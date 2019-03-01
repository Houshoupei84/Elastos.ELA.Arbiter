package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/elastos/Elastos.ELA/account"
	"github.com/elastos/Elastos.ELA/core/contract/program"
	"math"
	"math/rand"
	"strconv"

	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/base"
	"github.com/elastos/Elastos.ELA.Arbiter/config"
	"github.com/elastos/Elastos.ELA.Arbiter/rpc"

	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/core/types"
	"github.com/elastos/Elastos.ELA/core/types/payload"
	"github.com/elastos/Elastos.ELA/crypto"
)

type Transfer struct {
	Address string
	Amount  *common.Fixed64
}

type UTXO struct {
	Op       *types.OutPoint
	Amount   *common.Fixed64
	LockTime uint32
}

type KeyAddress struct {
	Name string
	Addr *Address
}

var wallet Wallet // Single instance of wallet

type Wallet interface {
	OpenKeystore(name string, password []byte) error

	GetAddresses() []*KeyAddress
	GetAddress(keystoreFile string) *KeyAddress
	GetAddressUTXOs(programHash *common.Uint168) ([]*UTXO, error)

	CreateTransaction(txType types.TxType, txPayload types.Payload, fromAddress, toAddress string, amount, fee *common.Fixed64, redeemScript []byte, currentHeight uint32) (*types.Transaction, error)
	CreateAuxpowTransaction(txType types.TxType, txPayload types.Payload, fromAddress string, fee *common.Fixed64, redeemScript []byte, currentHeight uint32) (*types.Transaction, error)
	CreateLockedTransaction(txType types.TxType, txPayload types.Payload, fromAddress, toAddress string, amount, fee *common.Fixed64, redeemScript []byte, lockedUntil uint32, currentHeight uint32) (*types.Transaction, error)
	CreateMultiOutputTransaction(fromAddress string, fee *common.Fixed64, redeemScript []byte, currentHeight uint32, output ...*Transfer) (*types.Transaction, error)
	CreateLockedMultiOutputTransaction(txType types.TxType, txPayload types.Payload, fromAddress string, fee *common.Fixed64, redeemScript []byte, lockedUntil uint32, currentHeight uint32, output ...*Transfer) (*types.Transaction, error)

	Sign(name string, password []byte, transaction *types.Transaction) (*types.Transaction, error)
}

type WalletImpl struct {
	Keystore

	keys []*KeyAddress
}

func Open(passwd []byte) (Wallet, error) {
	var keys []*KeyAddress
	var keystoreFiles []string
	keystoreFiles = append(keystoreFiles, DefaultKeystoreFile)
	for _, side := range config.Parameters.SideNodeList {
		keystoreFiles = append(keystoreFiles, side.KeystoreFile)
	}

	for _, keystore := range keystoreFiles {
		ks, err := OpenKeystore(keystore, passwd)
		if err != nil {
			return nil, errors.New("Side node keystore file open failed:" + err.Error())
		}
		address := ks.Address()
		hash, err := common.Uint168FromAddress(ks.Address())
		if err != nil {
			return nil, errors.New("Side chain invalid address:" + err.Error())
		}
		script := ks.GetRedeemScript()
		keys = append(keys, &KeyAddress{
			Name: keystore,
			Addr: &Address{
				Address:      address,
				ProgramHash:  hash,
				RedeemScript: script,
				Type:         TypeStand,
			}})
	}

	if wallet == nil {
		wallet = &WalletImpl{
			keys: keys,
		}
	}
	return wallet, nil
}

func (wallet *WalletImpl) OpenKeystore(name string, password []byte) error {
	keyStore, err := OpenKeystore(name, password)
	if err != nil {
		return err
	}
	wallet.Keystore = keyStore
	return nil
}

func (wallet *WalletImpl) CreateTransaction(txType types.TxType, txPayload types.Payload, fromAddress, toAddress string, amount, fee *common.Fixed64, redeemScript []byte, currentHeight uint32) (*types.Transaction, error) {
	return wallet.CreateLockedTransaction(txType, txPayload, fromAddress, toAddress, amount, fee, redeemScript, uint32(0), currentHeight)
}

func (wallet *WalletImpl) CreateLockedTransaction(txType types.TxType, txPayload types.Payload, fromAddress, toAddress string, amount, fee *common.Fixed64, redeemScript []byte, lockedUntil uint32, currentHeight uint32) (*types.Transaction, error) {
	return wallet.CreateLockedMultiOutputTransaction(txType, txPayload, fromAddress, fee, redeemScript, lockedUntil, currentHeight, &Transfer{toAddress, amount})
}

func (wallet *WalletImpl) CreateMultiOutputTransaction(fromAddress string, fee *common.Fixed64, redeemScript []byte, currentHeight uint32, outputs ...*Transfer) (*types.Transaction, error) {
	txType := types.TransferAsset
	txPayload := &payload.PayloadTransferAsset{}
	return wallet.CreateLockedMultiOutputTransaction(txType, txPayload, fromAddress, fee, redeemScript, uint32(0), currentHeight, outputs...)
}

func (wallet *WalletImpl) CreateLockedMultiOutputTransaction(txType types.TxType, txPayload types.Payload, fromAddress string, fee *common.Fixed64, redeemScript []byte, lockedUntil uint32, currentHeight uint32, outputs ...*Transfer) (*types.Transaction, error) {
	return wallet.createTransaction(txType, txPayload, fromAddress, fee, redeemScript, lockedUntil, currentHeight, outputs...)
}

func (wallet *WalletImpl) CreateAuxpowTransaction(txType types.TxType, txPayload types.Payload, fromAddress string, fee *common.Fixed64, redeemScript []byte, currentHeight uint32) (*types.Transaction, error) {
	// Check if from address is valid
	spender, err := common.Uint168FromAddress(fromAddress)
	if err != nil {
		return nil, errors.New(fmt.Sprint("[Wallet], Invalid spender address: ", fromAddress, ", error: ", err))
	}
	// Create transaction outputs
	var totalOutputAmount = common.Fixed64(0) // The total amount will be spend
	var txOutputs []*types.Output             // The outputs in transaction
	totalOutputAmount += *fee                 // Add transaction fee

	// Get spender's UTXOs
	UTXOs, err := wallet.GetAddressUTXOs(spender)
	if err != nil {
		return nil, errors.New("[Wallet], Get spender's UTXOs failed")
	}
	availableUTXOs := wallet.removeLockedUTXOs(UTXOs, currentHeight) // Remove locked UTXOs
	availableUTXOs = SortUTXOs(availableUTXOs)                       // Sort available UTXOs by value ASC

	// Create transaction inputs
	var txInputs []*types.Input // The inputs in transaction
	for _, utxo := range availableUTXOs {
		input := &types.Input{
			Previous: types.OutPoint{
				TxID:  utxo.Op.TxID,
				Index: utxo.Op.Index,
			},
			Sequence: utxo.LockTime,
		}
		txInputs = append(txInputs, input)
		if *utxo.Amount < totalOutputAmount {
			totalOutputAmount -= *utxo.Amount
		} else if *utxo.Amount == totalOutputAmount {
			totalOutputAmount = 0
			break
		} else if *utxo.Amount > totalOutputAmount {
			change := &types.Output{
				AssetID:     base.SystemAssetId,
				Value:       *utxo.Amount - totalOutputAmount,
				OutputLock:  uint32(0),
				ProgramHash: *spender,
			}
			txOutputs = append(txOutputs, change)
			totalOutputAmount = 0
			break
		}
	}
	if totalOutputAmount > 0 {
		return nil, errors.New("[Wallet], Available token is not enough")
	}

	// Check if output is valid add output with 0 amount to from address
	if len(txOutputs) == 0 {
		txOutput := &types.Output{
			AssetID:     base.SystemAssetId,
			ProgramHash: *spender,
			Value:       common.Fixed64(0),
			OutputLock:  uint32(0),
		}
		txOutputs = append(txOutputs, txOutput)
	}

	return wallet.newTransaction(txType, txPayload, redeemScript, txInputs, txOutputs, currentHeight), nil
}

func (wallet *WalletImpl) createTransaction(txType types.TxType, txPayload types.Payload, fromAddress string, fee *common.Fixed64, redeemScript []byte, lockedUntil uint32, currentHeight uint32, outputs ...*Transfer) (*types.Transaction, error) {
	// Check if output is valid
	if len(outputs) == 0 {
		return nil, errors.New("[Wallet], Invalid transaction target")
	}
	// Check if from address is valid
	spender, err := common.Uint168FromAddress(fromAddress)
	if err != nil {
		return nil, errors.New(fmt.Sprint("[Wallet], Invalid spender address: ", fromAddress, ", error: ", err))
	}
	// Create transaction outputs
	var totalOutputAmount = common.Fixed64(0) // The total amount will be spend
	var txOutputs []*types.Output             // The outputs in transaction
	totalOutputAmount += *fee                 // Add transaction fee

	for _, output := range outputs {
		receiver, err := common.Uint168FromAddress(output.Address)
		if err != nil {
			return nil, errors.New(fmt.Sprint("[Wallet], Invalid receiver address: ", output.Address, ", error: ", err))
		}
		txOutput := &types.Output{
			AssetID:     base.SystemAssetId,
			ProgramHash: *receiver,
			Value:       *output.Amount,
			OutputLock:  lockedUntil,
		}
		totalOutputAmount += *output.Amount
		txOutputs = append(txOutputs, txOutput)
	}
	// Get spender's UTXOs
	UTXOs, err := wallet.GetAddressUTXOs(spender)
	if err != nil {
		return nil, errors.New("[Wallet], Get spender's UTXOs failed")
	}
	availableUTXOs := wallet.removeLockedUTXOs(UTXOs, currentHeight) // Remove locked UTXOs
	availableUTXOs = SortUTXOs(availableUTXOs)                       // Sort available UTXOs by value ASC

	// Create transaction inputs
	var txInputs []*types.Input // The inputs in transaction
	for _, utxo := range availableUTXOs {
		input := &types.Input{
			Previous: types.OutPoint{
				TxID:  utxo.Op.TxID,
				Index: utxo.Op.Index,
			},
			Sequence: utxo.LockTime,
		}
		txInputs = append(txInputs, input)
		if *utxo.Amount < totalOutputAmount {
			totalOutputAmount -= *utxo.Amount
		} else if *utxo.Amount == totalOutputAmount {
			totalOutputAmount = 0
			break
		} else if *utxo.Amount > totalOutputAmount {
			change := &types.Output{
				AssetID:     base.SystemAssetId,
				Value:       *utxo.Amount - totalOutputAmount,
				OutputLock:  uint32(0),
				ProgramHash: *spender,
			}
			txOutputs = append(txOutputs, change)
			totalOutputAmount = 0
			break
		}
	}
	if totalOutputAmount > 0 {
		return nil, errors.New("[Wallet], Available token is not enough")
	}

	return wallet.newTransaction(txType, txPayload, redeemScript, txInputs, txOutputs, currentHeight), nil
}

func (wallet *WalletImpl) Sign(name string, password []byte, txn *types.Transaction) (*types.Transaction, error) {
	// Verify password
	err := wallet.OpenKeystore(name, password)
	if err != nil {
		return nil, err
	}
	// Get sign type
	signType, err := crypto.GetScriptType(txn.Programs[0].Code)
	if err != nil {
		return nil, err
	}
	// Look up transaction type
	if signType == common.STANDARD {

		// Sign single transaction
		txn, err = wallet.signStandardTransaction(password, txn)
		if err != nil {
			return nil, err
		}

	} else if signType == common.MULTISIG {

		// Sign multi sign transaction
		txn, err = wallet.signMultiSignTransaction(password, txn)
		if err != nil {
			return nil, err
		}
	}

	return txn, nil
}

func (wallet *WalletImpl) signStandardTransaction(password []byte, txn *types.Transaction) (*types.Transaction, error) {
	code := txn.Programs[0].Code
	// Get signer
	programHash := common.ToProgramHash(common.PrefixStandard, code)
	// Check if current user is a valid signer
	if *programHash != *wallet.Keystore.GetProgramHash() {
		return nil, errors.New("[Wallet], Invalid signer")
	}
	// Sign transaction
	signedTx, err := wallet.Keystore.Sign(password, txn)
	if err != nil {
		return nil, err
	}
	// Add verify program for transaction
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(len(signedTx)))
	buf.Write(signedTx)
	// Add signature
	txn.Programs[0].Parameter = buf.Bytes()

	return txn, nil
}

func (wallet *WalletImpl) signMultiSignTransaction(password []byte, txn *types.Transaction) (*types.Transaction, error) {
	code := txn.Programs[0].Code
	param := txn.Programs[0].Parameter
	// Check if current user is a valid signer
	var signerIndex = -1
	codeHashes, err := account.GetSigners(code)
	if err != nil {
		return nil, err
	}
	userCodeHash := wallet.Keystore.GetProgramHash().ToCodeHash()
	for i, programHash := range codeHashes {
		if programHash.IsEqual(userCodeHash) {
			signerIndex = i
			break
		}
	}
	if signerIndex == -1 {
		return nil, errors.New("[Wallet], Invalid multi sign signer")
	}
	// Sign transaction
	signature, err := wallet.Keystore.Sign(password, txn)
	if err != nil {
		return nil, err
	}
	// Append signature
	buf := new(bytes.Buffer)
	txn.SerializeUnsigned(buf)
	txn.Programs[0].Parameter, err = crypto.AppendSignature(signerIndex, signature, buf.Bytes(), code, param)
	if err != nil {
		return nil, err
	}

	return txn, nil
}

func (wallet *WalletImpl) removeLockedUTXOs(utxos []*UTXO, currentHeight uint32) []*UTXO {
	var availableUTXOs []*UTXO
	for _, utxo := range utxos {
		if utxo.LockTime > 0 {
			if utxo.LockTime >= currentHeight {
				continue
			}
			utxo.LockTime = math.MaxUint32 - 1
		}
		availableUTXOs = append(availableUTXOs, utxo)
	}
	return availableUTXOs
}

func (wallet *WalletImpl) newTransaction(txType types.TxType, txPayload types.Payload, redeemScript []byte, inputs []*types.Input, outputs []*types.Output, currentHeight uint32) *types.Transaction {
	// Create payload
	// txPayload = &payload.TransferAsset{}
	// Create attributes
	txAttr := types.NewAttribute(types.Nonce, []byte(strconv.FormatInt(rand.Int63(), 10)))
	attributes := make([]*types.Attribute, 0)
	attributes = append(attributes, &txAttr)
	// Create program
	var p = &program.Program{redeemScript, nil}
	// Create transaction
	return &types.Transaction{
		TxType:     txType,
		Payload:    txPayload,
		Attributes: attributes,
		Inputs:     inputs,
		Outputs:    outputs,
		Programs:   []*program.Program{p},
		LockTime:   currentHeight - 1,
	}
}

func (wallet *WalletImpl) GetAddresses() []*KeyAddress {
	return wallet.keys
}

func (wallet *WalletImpl) GetAddress(keystoreFile string) *KeyAddress {
	for _, ks := range wallet.keys {
		if ks.Name == keystoreFile {
			return ks
		}
	}
	return nil
}

func (wallet *WalletImpl) GetAddressUTXOs(programHash *common.Uint168) ([]*UTXO, error) {
	address, err := programHash.ToAddress()
	if err != nil {
		return nil, err
	}

	utxoInfos, err := rpc.GetUnspentUtxo([]string{address}, config.Parameters.MainNode.Rpc)
	if err != nil {
		return nil, err
	}

	var inputs []*UTXO
	for _, utxoInfo := range utxoInfos {

		bytes, err := common.HexStringToBytes(utxoInfo.Txid)
		if err != nil {
			return nil, err
		}
		reversedBytes := common.BytesReverse(bytes)
		txid, err := common.Uint256FromBytes(reversedBytes)
		if err != nil {
			return nil, err
		}

		var op types.OutPoint
		op.TxID = *txid
		op.Index = uint16(utxoInfo.VOut)

		amount, err := common.StringToFixed64(utxoInfo.Amount)
		if err != nil {
			return nil, err
		}

		inputs = append(inputs, &UTXO{&op, amount, utxoInfo.OutputLock})
	}
	return inputs, nil
}
