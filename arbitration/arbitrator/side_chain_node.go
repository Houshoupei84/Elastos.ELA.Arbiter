package arbitrator

import (
	. "Elastos.ELA.Arbiter/arbitration/base"
)

type SideChainNode interface {
	GetCurrentHeight() (uint32, error)
	GetBlockByHeight(height uint32) (*BlockInfo, error)

	SendTransaction(info *TransactionInfo) error
}
