package arbitrator

import (
	. "github.com/elastos/Elastos.ELA.Arbiter/arbitration/base"
	. "github.com/elastos/Elastos.ELA.Arbiter/store"

	"github.com/elastos/Elastos.ELA/core"
)

type MainChain interface {
	CreateWithdrawTransaction(sideChain SideChain, withdrawInfo *WithdrawInfo,
		sideChainTransactionHashes []string, mcFunc MainChainFunc) (*core.Transaction, error)
	ParseUserDepositTransactionInfo(txn *core.Transaction, genesisAddress string) (*DepositInfo, error)

	BroadcastWithdrawProposal(txn *core.Transaction) error
	ReceiveProposalFeedback(content []byte) error

	SyncMainChainCachedTxs() error
	CheckAndRemoveDepositTransactionsFromDB() error
	SyncChainData()
}

type MainChainClient interface {
	OnReceivedProposal(content []byte) error
}

type MainChainFunc interface {
	GetAvailableUtxos(withdrawBank string) ([]*AddressUTXO, error)
	GetMainNodeCurrentHeight() (uint32, error)
}
