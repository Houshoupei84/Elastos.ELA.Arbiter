package sidechain

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strconv"
	"sync"

	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/arbitrator"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/base"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/cs"
	"github.com/elastos/Elastos.ELA.Arbiter/config"
	"github.com/elastos/Elastos.ELA.Arbiter/log"
	"github.com/elastos/Elastos.ELA.Arbiter/rpc"
	"github.com/elastos/Elastos.ELA.Arbiter/sideauxpow"
	"github.com/elastos/Elastos.ELA.Arbiter/store"

	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/core/types"
	dpeer "github.com/elastos/Elastos.ELA/dpos/p2p/peer"
)

type SideChainImpl struct {
	mux         sync.Mutex
	withdrawMux sync.Mutex

	Key           string
	CurrentConfig *config.SideNodeConfig

	LastUsedUtxoHeight        uint32
	LastUsedOutPoints         []types.OutPoint
	ToSendTransactionHashes   map[uint32][]string
	ToSendTransactionsHeight  uint32
	Ready                     bool
	ReceivedUsedUtxoMsgNumber uint32
}

func (sc *SideChainImpl) OnGetLastArbiterUsedUTXOMessage(id dpeer.PID, content []byte) {
	buf := new(bytes.Buffer)
	buf.Write(content)
	msg := &cs.GetLastArbiterUsedUTXOMessage{}
	msg.Deserialize(buf)

	if err := sc.ReceiveGetLastArbiterUsedUtxos(id, msg.Height, msg.GenesisAddress); err != nil {
		log.Error("[OnGetLastArbiterUsedUTXOMessage] sidechain get last arbiter used utxo message error: ", err)
	}
}

func (sc *SideChainImpl) OnSendLastArbiterUsedUTXOMessage(id dpeer.PID, content []byte) {
	buf := new(bytes.Buffer)
	buf.Write(content)
	msg := &cs.SendLastArbiterUsedUTXOMessage{}
	msg.Deserialize(buf)

	if err := sc.ReceiveSendLastArbiterUsedUtxos(msg.Height, msg.GenesisAddress, msg.OutPoints); err != nil {
		log.Error("[OnGetLastArbiterUsedUTXOMessage] sidechain send last arbiter used utxo message error: ", err)
	}
}

func (sc *SideChainImpl) ReceiveSendLastArbiterUsedUtxos(height uint32, genesisAddress string, outPoints []types.OutPoint) error {
	log.Debug("[ReceiveSendLastArbiterUsedUtxos] start")
	defer log.Debug("[ReceiveSendLastArbiterUsedUtxos] end")

	sc.withdrawMux.Lock()
	defer sc.withdrawMux.Unlock()

	sc.mux.Lock()
	scKey := sc.GetKey()
	scHeight := sc.ToSendTransactionsHeight
	ready := sc.Ready
	txs := sc.ToSendTransactionHashes
	sc.mux.Unlock()
	log.Info("[ReceiveSendLastArbiterUsedUtxos] Received message, scKey", scKey, "genesisAddress:", genesisAddress)
	log.Info("[ReceiveSendLastArbiterUsedUtxos] Received message, received height:", height, "my height:", sc.LastUsedUtxoHeight)
	if scKey == genesisAddress && scHeight <= height {
		sc.mux.Lock()
		sc.ReceivedUsedUtxoMsgNumber++
		msgNum := sc.ReceivedUsedUtxoMsgNumber
		sc.mux.Unlock()
		sc.AddLastUsedOutPoints(outPoints)
		sc.SetLastUsedUtxoHeight(height)
		if ready && msgNum >= config.Parameters.MinReceivedUsedUtxoMsgNumber {
			for _, v := range txs {
				err := sc.CreateAndBroadcastWithdrawProposal(v)
				if err != nil {
					log.Error("[ReceiveSendLastArbiterUsedUtxos] CreateAndBroadcastWithdrawProposal failed")
				}
			}
			sc.mux.Lock()
			sc.Ready = false
			sc.ToSendTransactionHashes = make(map[uint32][]string, 0)
			sc.ToSendTransactionsHeight = 0
			sc.mux.Unlock()
			log.Info("[ReceiveSendLastArbiterUsedUtxos] Send transactions for multi sign")
		}
	}
	return nil
}

func (sc *SideChainImpl) ReceiveGetLastArbiterUsedUtxos(id dpeer.PID, height uint32, genesisAddress string) error {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	if sc.GetKey() == genesisAddress {
		log.Info("[ReceiveGetLastArbiterUsedUtxos] Received message, need height:", height, "my height:", sc.LastUsedUtxoHeight)
		if sc.LastUsedUtxoHeight >= height {
			var number = make([]byte, 8)
			var nonce int64
			rand.Read(number)
			binary.Read(bytes.NewReader(number), binary.LittleEndian, &nonce)

			msg := &cs.SendLastArbiterUsedUTXOMessage{
				Command:        cs.SendLastArbiterUsedUtxoCommand,
				GenesisAddress: genesisAddress,
				Height:         sc.LastUsedUtxoHeight,
				OutPoints:      sc.LastUsedOutPoints,
				Nonce:          strconv.FormatInt(nonce, 10),
			}
			cs.P2PClientSingleton.SendMessageToPeer(id, msg)

			utxos, err := store.DbCache.UTXOStore.GetAddressUTXOsFromGenesisBlockAddress(genesisAddress)
			if err != nil {
				return err
			}
			var newOutPoints []types.OutPoint
			for _, op := range sc.LastUsedOutPoints {
				isContained := false
				for _, utxo := range utxos {
					if op.IsEqual(utxo.Input.Previous) {
						isContained = true
					}
				}
				if !isContained {
					newOutPoints = append(newOutPoints, op)
				}
			}
			sc.LastUsedOutPoints = newOutPoints
		} else {
			return errors.New("I have no needed outpoints at requested height")
		}
	}

	return nil
}

func (sc *SideChainImpl) GetKey() string {
	return sc.Key
}

func (sc *SideChainImpl) GetLastUsedUtxoHeight() uint32 {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	return sc.LastUsedUtxoHeight
}

func (sc *SideChainImpl) SetLastUsedUtxoHeight(height uint32) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	sc.LastUsedUtxoHeight = height
}

func (sc *SideChainImpl) GetLastUsedOutPoints() []types.OutPoint {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	return sc.LastUsedOutPoints
}

func (sc *SideChainImpl) AddLastUsedOutPoints(ops []types.OutPoint) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	for _, op := range ops {
		isContained := false
		for _, outPoint := range sc.LastUsedOutPoints {
			if op.IsEqual(outPoint) {
				isContained = true
			}
		}
		if !isContained {
			sc.LastUsedOutPoints = append(sc.LastUsedOutPoints, op)
		}
	}
}

func (sc *SideChainImpl) RemoveLastUsedOutPoints(ops []types.OutPoint) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	var newOutPoints []types.OutPoint
	for _, outPoint := range sc.LastUsedOutPoints {
		isContained := false
		for _, op := range ops {
			if outPoint.IsEqual(op) {
				isContained = true
			}
		}
		if !isContained {
			newOutPoints = append(newOutPoints, outPoint)
		}
	}
	sc.LastUsedOutPoints = newOutPoints
}

func (sc *SideChainImpl) ClearLastUsedOutPoints() {
	sc.mux.Lock()
	defer sc.mux.Unlock()

	sc.LastUsedOutPoints = make([]types.OutPoint, 0)
}

func (sc *SideChainImpl) getCurrentConfig() *config.SideNodeConfig {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	if sc.CurrentConfig == nil {
		for _, sideConfig := range config.Parameters.SideNodeList {
			if sc.GetKey() == sideConfig.GenesisBlockAddress {
				sc.CurrentConfig = sideConfig
				break
			}
		}
	}
	return sc.CurrentConfig
}

func (sc *SideChainImpl) GetExchangeRate() (float64, error) {
	con := sc.getCurrentConfig()
	if con == nil {
		return 0, errors.New("get exchange rate failed, side chain has no config")
	}
	if sc.getCurrentConfig().ExchangeRate <= 0 {
		return 0, errors.New("get exchange rate failed, invalid exchange rate")
	}

	return sc.getCurrentConfig().ExchangeRate, nil
}

func (sc *SideChainImpl) GetCurrentHeight() (uint32, error) {
	return rpc.GetCurrentHeight(sc.getCurrentConfig().Rpc)
}

func (sc *SideChainImpl) GetBlockByHeight(height uint32) (*base.BlockInfo, error) {
	return rpc.GetBlockByHeight(height, sc.getCurrentConfig().Rpc)
}

func (sc *SideChainImpl) SendTransaction(txHash *common.Uint256) (rpc.Response, error) {
	log.Info("[Rpc-sendtransactioninfo] Deposit transaction to side chain：", sc.CurrentConfig.Rpc.IpAddress, ":", sc.CurrentConfig.Rpc.HttpJsonPort)
	response, err := rpc.CallAndUnmarshalResponse("sendrechargetransaction", rpc.Param("txid", txHash.String()), sc.CurrentConfig.Rpc)
	if err != nil {
		return rpc.Response{}, err
	}
	log.Info("[Rpc-sendtransactioninfo] Deposit transaction finished")

	if response.Error != nil {
		log.Info("response: ", response.Error.Message)
	} else {
		log.Info("response:", response)
	}

	return response, nil
}

func (sc *SideChainImpl) GetAccountAddress() string {
	return sc.GetKey()
}

func (sc *SideChainImpl) OnUTXOChanged(txinfos []*base.WithdrawTx, blockHeight uint32) error {
	if len(txinfos) == 0 {
		return errors.New("[OnUTXOChanged] received txinfos, but size is 0")
	}

	var txs []*base.SideChainTransaction
	for _, txinfo := range txinfos {
		buf := new(bytes.Buffer)
		if err := txinfo.Serialize(buf); err != nil {
			return errors.New("[OnUTXOChanged] received txinfos, but have invalid tx," + err.Error())
		}

		txs = append(txs, &base.SideChainTransaction{
			TransactionHash:     txinfo.Txid.String(),
			GenesisBlockAddress: sc.GetKey(),
			Transaction:         buf.Bytes(),
			BlockHeight:         blockHeight,
		})
	}

	if err := store.DbCache.SideChainStore.AddSideChainTxs(txs); err != nil {
		return err
	}

	log.Info("[OnUTXOChanged] Find ", len(txs), "withdraw transaction, add into dbcache")
	return nil
}

func (sc *SideChainImpl) OnIllegalEvidenceFound(evidence *types.SidechainIllegalData) error {
	arbitrator.ArbitratorGroupSingleton.GetCurrentArbitrator().BroadcastSidechainIllegalData(evidence)
	return nil
}

func (sc *SideChainImpl) StartSideChainMining() {
	if sc.CurrentConfig.PowChain {
		log.Info("[OnDutyChanged] Start side chain mining: genesis address [", sc.Key, "]")
		sideauxpow.StartSideChainMining(sc.CurrentConfig)
	} else {
		log.Debug("[StartSideChainMining] side chain is not pow chain, no need to mining")
	}
}

func (sc *SideChainImpl) SubmitAuxpow(genesishash string, blockhash string, submitauxpow string) error {
	return sideauxpow.SubmitAuxpow(genesishash, blockhash, submitauxpow)
}

func (sc *SideChainImpl) UpdateLastNotifySideMiningHeight(genesisBlockHash common.Uint256) {
	sideauxpow.UpdateLastNotifySideMiningHeight(genesisBlockHash)
}

func (sc *SideChainImpl) UpdateLastSubmitAuxpowHeight(genesisBlockHash common.Uint256) {
	sideauxpow.UpdateLastSubmitAuxpowHeight(genesisBlockHash)
}

func (sc *SideChainImpl) GetExistDepositTransactions(txs []string) ([]string, error) {
	receivedTxs, err := rpc.GetExistDepositTransactions(txs, sc.CurrentConfig.Rpc)
	if err != nil {
		return nil, err
	}
	return receivedTxs, nil
}

func (sc *SideChainImpl) GetWithdrawTransaction(txHash string) (*base.WithdrawTxInfo, error) {
	txInfo, err := rpc.GetTransactionInfoByHash(txHash, sc.CurrentConfig.Rpc)
	if err != nil {
		return nil, err
	}

	return txInfo, nil
}

func (sc *SideChainImpl) CheckIllegalEvidence(evidence *base.SidechainIllegalDataInfo) (bool, error) {
	return rpc.CheckIllegalEvidence(evidence, sc.CurrentConfig.Rpc)
}

func (sc *SideChainImpl) parseUserWithdrawTransactionInfo(txs []*base.WithdrawTx) (*base.WithdrawInfo, error) {
	result := new(base.WithdrawInfo)
	for _, tx := range txs {
		for _, withdraw := range tx.WithdrawInfo.WithdrawAssets {
			result.WithdrawAssets = append(result.WithdrawAssets, withdraw)
		}
	}
	return result, nil
}

func (sc *SideChainImpl) SendCachedWithdrawTxs() {
	log.Info("[SendCachedWithdrawTxs] start")
	defer log.Info("[SendCachedWithdrawTxs] end")

	txHashes, blockHeights, err := store.DbCache.SideChainStore.GetAllSideChainTxHashesAndHeights(sc.GetKey())
	if err != nil {
		log.Errorf("[SendCachedWithdrawTxs] %s", err.Error())
		return
	}

	if len(txHashes) == 0 {
		log.Info("No cached withdraw transaction need to send")
		return
	}

	receivedTxs, err := rpc.GetExistWithdrawTransactions(txHashes)
	if err != nil {
		log.Errorf("[SendCachedWithdrawTxs] %s", err.Error())
		return
	}

	unsolvedTxs, unsolvedBlockHeights := base.SubstractTransactionHashesAndBlockHeights(txHashes, blockHeights, receivedTxs)

	chainHeight, err := rpc.GetCurrentHeight(config.Parameters.MainNode.Rpc)
	if err != nil {
		log.Errorf("[SendCachedWithdrawTxs] %s", err.Error())
		return
	}

	if len(unsolvedTxs) != 0 {
		heightTxsMap := base.GetHeightTransactionHashesMap(unsolvedTxs, unsolvedBlockHeights)

		sc.ToSendTransactionHashes = heightTxsMap
		sc.ToSendTransactionsHeight = chainHeight - 1
		sc.Ready = true
		sc.ReceivedUsedUtxoMsgNumber = 0

		var number = make([]byte, 8)
		var nonce int64
		rand.Read(number)
		binary.Read(bytes.NewReader(number), binary.LittleEndian, &nonce)

		msg := &cs.GetLastArbiterUsedUTXOMessage{
			Command:        cs.GetLastArbiterUsedUtxoCommand,
			GenesisAddress: sc.GetKey(),
			Height:         chainHeight - 1,
			Nonce:          strconv.FormatInt(nonce, 10)}
		cs.P2PClientSingleton.BroadcastMessage(msg)
		log.Info("[SendCachedWithdrawTxs] Find withdraw transaction, send GetLastArbiterUsedUtxoCommand mssage")
	}

	if len(receivedTxs) != 0 {
		err = store.DbCache.SideChainStore.RemoveSideChainTxs(receivedTxs)
		if err != nil {
			log.Errorf("[SendCachedWithdrawTxs] %s", err.Error())
			return
		}

		err = store.FinishedTxsDbCache.AddSucceedWithdrawTxs(receivedTxs)
		if err != nil {
			log.Errorf("[SendCachedWithdrawTxs] %s", err.Error())
			return
		}
	}
}

func (sc *SideChainImpl) CreateAndBroadcastWithdrawProposal(txnHashes []string) error {
	unsolvedTransactions, err := store.DbCache.SideChainStore.GetSideChainTxsFromHashes(txnHashes)
	if err != nil {
		return err
	}

	if len(unsolvedTransactions) == 0 {
		return nil
	}

	withdrawInfo, err := sc.parseUserWithdrawTransactionInfo(unsolvedTransactions)
	if err != nil {
		return err
	}

	currentArbitrator := arbitrator.ArbitratorGroupSingleton.GetCurrentArbitrator()
	currentArbitrator.GetMainChain().SyncChainData()
	transactions := currentArbitrator.CreateWithdrawTransactions(withdrawInfo, sc, txnHashes, &arbitrator.DbMainChainFunc{})

	log.Info("[CreateAndBroadcastWithdrawProposal] Transactions count: ", len(transactions))
	currentArbitrator.BroadcastWithdrawProposal(transactions)

	return nil
}
