package transaction

import (
	"io"
	"fmt"

	. "Elastos.ELA.Arbiter/common"
	"Elastos.ELA.Arbiter/common/serialization"
)

type TxOutput struct {
	AssetID     Uint256
	Value       Fixed64
	OutputLock  uint32
	ProgramHash Uint168
}

func (self TxOutput) String() string {
	return "TxOutput: {\n\t\t" +
		"AssetID: " + self.AssetID.String() + "\n\t\t" +
		"Value: " + self.Value.String() + "\n\t\t" +
		"OutputLock: " + fmt.Sprint(self.OutputLock) + "\n\t\t" +
		"ProgramHash: " + self.ProgramHash.String() + "\n\t\t" +
		"}"
}

func (o *TxOutput) Serialize(w io.Writer) {
	o.AssetID.Serialize(w)
	o.Value.Serialize(w)
	serialization.WriteUint32(w, o.OutputLock)
	o.ProgramHash.Serialize(w)
}

func (o *TxOutput) Deserialize(r io.Reader) {
	o.AssetID.Deserialize(r)
	o.Value.Deserialize(r)
	temp, _ := serialization.ReadUint32(r)
	o.OutputLock = uint32(temp)
	o.ProgramHash.Deserialize(r)
}
