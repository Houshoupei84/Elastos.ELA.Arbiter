package sidechain

import (
	"errors"
	"sync"
	"time"

	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/arbitrator"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/base"
	"github.com/elastos/Elastos.ELA.Arbiter/config"
	"github.com/elastos/Elastos.ELA.Arbiter/log"
	"github.com/elastos/Elastos.ELA.Arbiter/rpc"
	"github.com/elastos/Elastos.ELA.Arbiter/store"

	"github.com/elastos/Elastos.ELA/common"
)

type SideChainAccountMonitorImpl struct {
	mux sync.Mutex

	ParentArbitrator   arbitrator.Arbitrator
	accountListenerMap map[string]base.AccountListener
}

func (monitor *SideChainAccountMonitorImpl) tryInit() {
	if monitor.accountListenerMap == nil {
		monitor.accountListenerMap = make(map[string]base.AccountListener)
	}
}

func (monitor *SideChainAccountMonitorImpl) AddListener(listener base.AccountListener) {
	monitor.tryInit()
	monitor.accountListenerMap[listener.GetAccountAddress()] = listener
}

func (monitor *SideChainAccountMonitorImpl) RemoveListener(account string) error {
	if monitor.accountListenerMap == nil {
		return nil
	}

	if _, ok := monitor.accountListenerMap[account]; !ok {
		return errors.New("do not exist listener")
	}
	delete(monitor.accountListenerMap, account)
	return nil
}

func (monitor *SideChainAccountMonitorImpl) fireUTXOChanged(txinfos []*base.WithdrawTx, genesisBlockAddress string, blockHeight uint32) error {
	if monitor.accountListenerMap == nil {
		return nil
	}

	item, ok := monitor.accountListenerMap[genesisBlockAddress]
	if !ok {
		return errors.New("fired unknown listener")
	}

	return item.OnUTXOChanged(txinfos, blockHeight)
}

func (monitor *SideChainAccountMonitorImpl) fireIllegalEvidenceFound(evidence *base.SidechainIllegalData) error {
	if monitor.accountListenerMap == nil {
		return nil
	}

	item, ok := monitor.accountListenerMap[evidence.GenesisBlockAddress]
	if !ok {
		return errors.New("fired unknown listener")
	}

	return item.OnIllegalEvidenceFound(evidence)
}

func (monitor *SideChainAccountMonitorImpl) SyncChainData(sideNode *config.SideNodeConfig) {
	for {
		chainHeight, currentHeight, needSync := monitor.needSyncBlocks(sideNode.GenesisBlockAddress, sideNode.Rpc)

		if needSync {
			log.Info("currentHeight:", currentHeight, " chainHeight:", chainHeight)
			for currentHeight < chainHeight {
				if currentHeight >= 6 {
					transactions, err := rpc.GetWithdrawTransactionByHeight(currentHeight+1-6, sideNode.Rpc)
					if err != nil {
						log.Error("get destroyed transaction at height:", currentHeight+1-6, "failed\n"+
							"rpc:", sideNode.Rpc.IpAddress, ":", sideNode.Rpc.HttpJsonPort, "\n"+
							"error:", err)
						break
					}
					monitor.processTransactions(transactions, sideNode.GenesisBlockAddress, currentHeight+1-6)
				}

				//todo call fireIllegalEvidenceFound if found illegal evidence from sidechain rpc

				// Update wallet height
				currentHeight = store.DbCache.SideChainStore.CurrentSideHeight(sideNode.GenesisBlockAddress, currentHeight+1)
				log.Info(" [SyncSideChain] Side chain [", sideNode.GenesisBlockAddress, "] height: ", currentHeight)
			}

			arbitrator.ArbitratorGroupSingleton.SyncFromMainNode()
			if arbitrator.ArbitratorGroupSingleton.GetCurrentArbitrator().IsOnDutyOfMain() {
				sideChain, ok := arbitrator.ArbitratorGroupSingleton.GetCurrentArbitrator().GetSideChainManager().GetChain(sideNode.GenesisBlockAddress)
				if ok {
					sideChain.StartSideChainMining()
					log.Info("[SyncSideChain] Start side chain mining, genesis address: [", sideNode.GenesisBlockAddress, "]")
				}
			}
		}

		time.Sleep(time.Millisecond * config.Parameters.SideChainMonitorScanInterval)
	}
}

func (monitor *SideChainAccountMonitorImpl) needSyncBlocks(genesisBlockAddress string, config *config.RpcConfig) (uint32, uint32, bool) {

	chainHeight, err := rpc.GetCurrentHeight(config)
	if err != nil {
		return 0, 0, false
	}

	currentHeight := store.DbCache.SideChainStore.CurrentSideHeight(genesisBlockAddress, store.QueryHeightCode)

	if currentHeight >= chainHeight {
		return chainHeight, currentHeight, false
	}

	return chainHeight, currentHeight, true
}

func (monitor *SideChainAccountMonitorImpl) processTransactions(transactions []*base.WithdrawTxInfo, genesisAddress string, blockHeight uint32) {
	var txInfos []*base.WithdrawTx
	for _, txn := range transactions {
		txnBytes, err := common.HexStringToBytes(txn.TxID)
		if err != nil {
			log.Warn("Find output to destroy address, but transaction hash to transaction bytes failed")
			continue
		}
		reversedTxnBytes := common.BytesReverse(txnBytes)
		hash, err := common.Uint256FromBytes(reversedTxnBytes)
		if err != nil {
			log.Warn("Find output to destroy address, but reversed transaction hash bytes to transaction hash failed")
			continue
		}

		var withdrawAssets []*base.WithdrawAsset
		for _, withdraw := range txn.CrossChainAssets {
			opAmount, err := common.StringToFixed64(withdraw.OutputAmount)
			if err != nil {
				log.Warn("Find output to destroy address, but have invlaid corss chain output amount")
				continue
			}
			csAmount, err := common.StringToFixed64(withdraw.CrossChainAmount)
			if err != nil {
				log.Warn("Find output to destroy address, but have invlaid corss chain amount")
				continue
			}

			withdrawAssets = append(withdrawAssets, &base.WithdrawAsset{
				TargetAddress:    withdraw.CrossChainAddress,
				Amount:           opAmount,
				CrossChainAmount: csAmount,
			})
		}

		withdrawTx := &base.WithdrawTx{
			Txid: hash,
			WithdrawInfo: &base.WithdrawInfo{
				WithdrawAssets: withdrawAssets,
			},
		}

		reversedTxnHash := common.BytesToHexString(reversedTxnBytes)
		if ok, err := store.DbCache.SideChainStore.HasSideChainTx(reversedTxnHash); err != nil || !ok {
			txInfos = append(txInfos, withdrawTx)
		}
	}
	if len(txInfos) != 0 {
		err := monitor.fireUTXOChanged(txInfos, genesisAddress, blockHeight)
		if err != nil {
			log.Error("[fireUTXOChanged] err:", err.Error())
		}
	}
}
