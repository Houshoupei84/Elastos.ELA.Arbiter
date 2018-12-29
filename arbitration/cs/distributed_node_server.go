package cs

import (
	"bytes"
	"errors"
	"sync"

	. "github.com/elastos/Elastos.ELA.Arbiter/arbitration/arbitrator"
	. "github.com/elastos/Elastos.ELA.Arbiter/arbitration/base"
	"github.com/elastos/Elastos.ELA.Arbiter/config"
	"github.com/elastos/Elastos.ELA.Arbiter/log"
	"github.com/elastos/Elastos.ELA.Arbiter/store"

	"github.com/elastos/Elastos.ELA/account"
	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/core/contract"
	"github.com/elastos/Elastos.ELA/core/types"
	"github.com/elastos/Elastos.ELA/core/types/payload"
	"github.com/elastos/Elastos.ELA/crypto"
)

const (
	MCErrDoubleSpend          int64 = 45010
	MCErrSidechainTxDuplicate int64 = 45012
)

type DistributedNodeServer struct {
	mux                  *sync.Mutex
	withdrawMux          *sync.Mutex
	P2pCommand           string
	unsolvedTransactions map[common.Uint256]*types.Transaction
}

func (dns *DistributedNodeServer) tryInit() {
	if dns.mux == nil {
		dns.mux = new(sync.Mutex)
	}
	if dns.withdrawMux == nil {
		dns.withdrawMux = new(sync.Mutex)
	}
	if dns.unsolvedTransactions == nil {
		dns.unsolvedTransactions = make(map[common.Uint256]*types.Transaction)
	}
}

func (dns *DistributedNodeServer) UnsolvedTransactions() map[common.Uint256]*types.Transaction {
	dns.mux.Lock()
	defer dns.mux.Unlock()
	return dns.unsolvedTransactions
}

func CreateRedeemScript() ([]byte, error) {
	var publicKeys []*crypto.PublicKey
	for _, arStr := range ArbitratorGroupSingleton.GetAllArbitrators() {
		temp, err := PublicKeyFromString(arStr)
		if err != nil {
			return nil, err
		}
		publicKeys = append(publicKeys, temp)
	}
	redeemScript, err := CreateWithdrawRedeemScript(
		getTransactionAgreementArbitratorsCount(), publicKeys)
	if err != nil {
		return nil, err
	}
	return redeemScript, nil
}

func getTransactionAgreementArbitratorsCount() int {
	return config.Parameters.WithdrawMajorityCount
}

func (dns *DistributedNodeServer) sendToArbitrator(content []byte) {
	msg := &SignMessage{
		Command: dns.P2pCommand,
		Content: content,
	}

	P2PClientSingleton.BroadcastMessage(msg)
	log.Info("[sendToArbitrator] Send withdraw transaction to arbtiers for multi sign")
}

func (dns *DistributedNodeServer) BroadcastWithdrawProposal(transaction *types.Transaction) error {

	proposal, err := dns.generateWithdrawProposal(transaction, &DistrubutedItemFuncImpl{})
	if err != nil {
		return err
	}

	dns.sendToArbitrator(proposal)

	return nil
}

func (dns *DistributedNodeServer) generateWithdrawProposal(transaction *types.Transaction, itemFunc DistrubutedItemFunc) ([]byte, error) {
	dns.tryInit()

	currentArbitrator := ArbitratorGroupSingleton.GetCurrentArbitrator()
	pkBuf, err := currentArbitrator.GetPublicKey().EncodePoint(true)
	if err != nil {
		return nil, err
	}
	programHash, err := contract.PublicKeyToStandardProgramHash(pkBuf)
	if err != nil {
		return nil, err
	}
	transactionItem := &DistributedItem{
		ItemContent:                 transaction,
		TargetArbitratorPublicKey:   currentArbitrator.GetPublicKey(),
		TargetArbitratorProgramHash: programHash,
	}
	transactionItem.InitScript(currentArbitrator)
	transactionItem.Sign(currentArbitrator, false, itemFunc)

	buf := new(bytes.Buffer)
	err = transactionItem.Serialize(buf)
	if err != nil {
		return nil, err
	}

	transaction.Programs[0].Parameter = transactionItem.GetSignedData()

	dns.mux.Lock()
	defer dns.mux.Unlock()

	if _, ok := dns.unsolvedTransactions[transaction.Hash()]; ok {
		return nil, errors.New("Transaction already in process.")
	}
	dns.unsolvedTransactions[transaction.Hash()] = transaction

	return buf.Bytes(), nil
}

func (dns *DistributedNodeServer) ReceiveProposalFeedback(content []byte) error {
	dns.tryInit()
	dns.withdrawMux.Lock()
	defer dns.withdrawMux.Unlock()

	transactionItem := DistributedItem{}
	transactionItem.Deserialize(bytes.NewReader(content))
	newSign, err := transactionItem.ParseFeedbackSignedData()
	if err != nil {
		return err
	}

	dns.mux.Lock()
	if dns.unsolvedTransactions == nil {
		dns.mux.Unlock()
		return errors.New("Can not find proposal.")
	}
	txn, ok := dns.unsolvedTransactions[transactionItem.ItemContent.Hash()]
	if !ok {
		dns.mux.Unlock()
		return errors.New("Can not find proposal.")
	}
	dns.mux.Unlock()

	var signerIndex = -1
	codeHashes, err := account.GetSigners(txn.Programs[0].Code)
	if err != nil {
		return err
	}
	userCodeHash := transactionItem.TargetArbitratorProgramHash.ToCodeHash()
	for i, programHash := range codeHashes {
		if userCodeHash.IsEqual(*programHash) {
			signerIndex = i
			break
		}
	}
	if signerIndex == -1 {
		return errors.New("Invalid multi sign signer")
	}

	signedCount, err := MergeSignToTransaction(newSign, signerIndex, txn)
	if err != nil {
		return err
	}

	if signedCount >= getTransactionAgreementArbitratorsCount() {
		dns.mux.Lock()
		delete(dns.unsolvedTransactions, txn.Hash())
		dns.mux.Unlock()

		withdrawPayload, ok := txn.Payload.(*payload.PayloadWithdrawFromSideChain)
		if !ok {
			return errors.New("Received proposal feed back but withdraw transaction has invalid payload")
		}

		currentArbitrator := ArbitratorGroupSingleton.GetCurrentArbitrator()
		resp, err := currentArbitrator.SendWithdrawTransaction(txn)

		var transactionHashes []string
		for _, hash := range withdrawPayload.SideChainTransactionHashes {
			transactionHashes = append(transactionHashes, hash.String())
		}

		if err != nil || resp.Error != nil && resp.Code != MCErrDoubleSpend {
			log.Warn("Send withdraw transaction failed, move to finished db, txHash:", txn.Hash().String())

			buf := new(bytes.Buffer)
			err := txn.Serialize(buf)
			if err != nil {
				return errors.New("Send withdraw transaction faild, invalid transaction")
			}

			err = store.DbCache.SideChainStore.RemoveSideChainTxs(transactionHashes)
			if err != nil {
				return errors.New("Remove failed withdraw transaction from db failed")
			}
			err = store.FinishedTxsDbCache.AddFailedWithdrawTxs(transactionHashes, buf.Bytes())
			if err != nil {
				return errors.New("Add failed withdraw transaction into finished db failed")
			}
		} else if resp.Error == nil && resp.Result != nil || resp.Error != nil && resp.Code == MCErrSidechainTxDuplicate {
			if resp.Error != nil {
				log.Info("Send withdraw transaction found has been processed, move to finished db, txHash:", txn.Hash().String())
			} else {
				log.Info("Send withdraw transaction succeed, move to finished db, txHash:", txn.Hash().String())
			}
			var newUsedUtxos []types.OutPoint
			for _, input := range txn.Inputs {
				newUsedUtxos = append(newUsedUtxos, input.Previous)
			}
			sidechain, ok := currentArbitrator.GetSideChainManager().GetChain(withdrawPayload.GenesisBlockAddress)
			if !ok {
				return errors.New("Get side chain from withdraw payload failed")
			}
			sidechain.AddLastUsedOutPoints(newUsedUtxos)

			err = store.DbCache.SideChainStore.RemoveSideChainTxs(transactionHashes)
			if err != nil {
				return errors.New("Remove succeed withdraw transaction from db failed")
			}
			err = store.FinishedTxsDbCache.AddSucceedWithdrawTxs(transactionHashes)
			if err != nil {
				return errors.New("Add succeed withdraw transaction into finished db failed")
			}
		} else {
			log.Warn("Send withdraw transaction failed, need to resend")
		}
	}
	return nil
}
