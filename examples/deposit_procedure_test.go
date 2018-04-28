package examples

import (
	"bytes"
	"fmt"

	"encoding/json"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/arbitrator"
	. "github.com/elastos/Elastos.ELA.Arbiter/arbitration/arbitrator"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/cs"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/mainchain"
	"github.com/elastos/Elastos.ELA.Arbiter/arbitration/sidechain"
	"github.com/elastos/Elastos.ELA.Arbiter/config"
	"github.com/elastos/Elastos.ELA.Arbiter/log"
	"github.com/elastos/Elastos.ELA.Utility/common"
	"github.com/elastos/Elastos.ELA/bloom"
	. "github.com/elastos/Elastos.ELA/core"
)

func init() {
	config.InitMockConfig()
	log.Init(log.Path, log.Stdout)
}

//This example demonstrate normal procedure of deposit
//As we known, the entire procedure will involve main chain, side chain, client of main chain
//	and client of side chain. To simplify this, we suppose the others are running well, and
//	we already known the result of these procedures.
func ExampleNormalDeposit() {

	//--------------Part1(On client of main chain)-------------------------
	//Step1.1 create transaction(tx1)
	//	./ela-cli wallet -t create -deposit EMmfgnrDLQmFPBJiWvsyYGV2jzLQY58J4Y --amount 10 --fee 1

	//Step1.2 sign tx1
	//	./ela-cli wallet -t sign --hex

	//Step1.3 send tx1 to main chain

	//--------------Part2(On main chain)-------------------------
	//Step2.1 tx1 has been confirmed and packaged in a new block

	//--------------Part3(On arbiter)-------------------------
	//let's suppose we get the object of current on duty arbitrator
	arbitrator := arbitrator.ArbitratorImpl{}
	mc := &mainchain.MainChainImpl{&cs.DistributedNodeServer{}}
	arbitrator.SetMainChain(mc)

	sideChainManager := &sidechain.SideChainManagerImpl{make(map[string]SideChain)}
	side := &sidechain.SideChainImpl{
		nil,
		"EMmfgnrDLQmFPBJiWvsyYGV2jzLQY58J4Y",
		nil,
	}
	sideChainManager.AddChain("EMmfgnrDLQmFPBJiWvsyYGV2jzLQY58J4Y", side)
	arbitrator.SetSideChainManager(sideChainManager)

	//Step3.1 spv module found the tx1, and fire Notify callback of TransactionListener

	//let's suppose we already known the serialized code of tx1 and proof of tx1 from the callback
	var strTx1 string
	strTx1 = "0800012245544d4751433561473131627752677553704357324e6b7950387a75544833486e3200010013353537373030363739313934373737393431300403229feeff99fa03357d09648a93363d1d01f234e61d04d10f93c9ad1aef3c150100feffffff737a4387ebf5315b74c508e40ba4f0179fc1d68bf76ce079b6bbf26e0fd2aa470100feffffff592c415c08ac1e1312d98cf6a28f68b62dd28ae964ed33af882b2d16b3a44a900100feffffff34255723e2249e8d965892edb9cd4cbbe27fa30e1292372a07206079dfad4a260100feffffff02b037db964a231458d2d6ffd5ea18944c4f90e63d547c5d3b9874df66a4ead0a300ca9a3b00000000000000002132a3f3d36f0db243743debee55155d5343322c2ab037db964a231458d2d6ffd5ea18944c4f90e63d547c5d3b9874df66a4ead0a3782e43120000000000000000216fd749255076c304942d16a8023a63b504b6022f570200000100232103c3ffe56a4c68b4dfe91573081898cb9a01830e48b8f181de684e415ecfc0e098ac"
	strProof := "5f894325400c9a12f4490da7bca9f4e32466f497a65aacb2dbfa29ac14619944b300000001000000010000005f894325400c9a12f4490da7bca9f4e32466f497a65aacb2dbfa29ac14619944fd83010800012245544d4751433561473131627752677553704357324e6b7950387a75544833486e3200010013353537373030363739313934373737393431300403229feeff99fa03357d09648a93363d1d01f234e61d04d10f93c9ad1aef3c150100feffffff737a4387ebf5315b74c508e40ba4f0179fc1d68bf76ce079b6bbf26e0fd2aa470100feffffff592c415c08ac1e1312d98cf6a28f68b62dd28ae964ed33af882b2d16b3a44a900100feffffff34255723e2249e8d965892edb9cd4cbbe27fa30e1292372a07206079dfad4a260100feffffff02b037db964a231458d2d6ffd5ea18944c4f90e63d547c5d3b9874df66a4ead0a300ca9a3b00000000000000002132a3f3d36f0db243743debee55155d5343322c2ab037db964a231458d2d6ffd5ea18944c4f90e63d547c5d3b9874df66a4ead0a3782e43120000000000000000216fd749255076c304942d16a8023a63b504b6022f570200000100232103c3ffe56a4c68b4dfe91573081898cb9a01830e48b8f181de684e415ecfc0e098ac"

	var tx1 *Transaction
	tx1 = new(Transaction)
	byteTx1, _ := common.HexStringToBytes(strTx1)
	txReader := bytes.NewReader(byteTx1)
	tx1.Deserialize(txReader)

	var proof bloom.MerkleProof
	byteProof, _ := common.HexStringToBytes(strProof)
	proofReader := bytes.NewReader(byteProof)
	proof.Deserialize(proofReader)

	//step3.2 parse deposit info from tx1
	depositInfos, _ := arbitrator.ParseUserDepositTransactionInfo(tx1)

	//step3.3 create transaction(tx2) info from deposit info
	transactionInfos := arbitrator.CreateDepositTransactions(proof, depositInfos)

	//step3.4 send tx2 info to side chain
	//arbitrator.SendDepositTransactions(transactionInfos)

	//--------------Part4(On side chain)-------------------------
	//step4.1 side chain node received tx2 info

	//let's suppose we already known the serialized tx2 info
	var serializedTx2 string
	for info := range transactionInfos {
		infoBytes, _ := json.Marshal(info)
		serializedTx2 = common.BytesToHexString(infoBytes)
	}

	//convert tx2 info to tx2

	//step4.2 special verify of tx2 which contains spv proof of tx1

	//step4.3 tx2 has been confirmed and packaged in a new block

	fmt.Printf("Length of transaction info array: [%d]\n"+
		"Serialized tx2: [%s]",
		len(transactionInfos), serializedTx2)

	// Output:
	// Length of transaction info array: [1]
	// Serialized tx2: [7b2274786964223a22222c2268617368223a22222c2273697a65223a302c227673697a65223a302c2276657273696f6e223a302c226c6f636b74696d65223a302c2276696e223a5b5d2c22766f7574223a5b7b2276616c7565223a223130222c226e223a302c2261646472657373223a2245544d4751433561473131627752677553704357324e6b7950387a75544833486e32222c2261737365746964223a2262303337646239363461323331343538643264366666643565613138393434633466393065363364353437633564336239383734646636366134656164306133222c226f75747075746c6f636b223a307d5d2c22626c6f636b68617368223a22222c22636f6e6669726d6174696f6e73223a302c2274696d65223a302c22626c6f636b74696d65223a302c2274797065223a362c227061796c6f616476657273696f6e223a302c227061796c6f6164223a7b2250726f6f66223a223566383934333235343030633961313266343439306461376263613966346533323436366634393761363561616362326462666132396163313436313939343462333030303030303031303030303030303130303030303035663839343332353430306339613132663434393064613762636139663465333234363666343937613635616163623264626661323961633134363139393434666438333031303830303031323234353534346434373531343333353631343733313331363237373532363737353533373034333537333234653662373935303338376137353534343833333438366533323030303130303133333533353337333733303330333633373339333133393334333733373337333933343331333030343033323239666565666639396661303333353764303936343861393333363364316430316632333465363164303464313066393363396164316165663363313530313030666566666666666637333761343338376562663533313562373463353038653430626134663031373966633164363862663736636530373962366262663236653066643261613437303130306665666666666666353932633431356330386163316531333132643938636636613238663638623632646432386165393634656433336166383832623264313662336134346139303031303066656666666666663334323535373233653232343965386439363538393265646239636434636262653237666133306531323932333732613037323036303739646661643461323630313030666566666666666630326230333764623936346132333134353864326436666664356561313839343463346639306536336435343763356433623938373464663636613465616430613330306361396133623030303030303030303030303030303032313332613366336433366630646232343337343364656265653535313535643533343333323263326162303337646239363461323331343538643264366666643565613138393434633466393065363364353437633564336239383734646636366134656164306133373832653433313230303030303030303030303030303030323136666437343932353530373663333034393432643136613830323361363362353034623630323266353730323030303030313030323332313033633366666535366134633638623464666539313537333038313839386362396130313833306534386238663138316465363834653431356563666330653039386163227d2c2261747472696275746573223a5b7b227573616765223a302c2264617461223a2235353737303036373931393437373739343130227d5d2c2270726f6772616d73223a5b7b22436f6465223a22222c22506172616d65746572223a22227d5d7d]
}
