package pb

import "testing"

func TestRoundTripFixed64(t *testing.T) {
	var e Encoder
	e.Fixed64Field(1, 117093590311632896)
	e.VarintField(2, 42)
	e.BoolField(3, true)
	e.StringField(4, "hi")
	fields, err := Decode(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("n=%d", len(fields))
	}
	if fields[0].Fixed64 != 117093590311632896 {
		t.Fatal(fields[0])
	}
	if fields[1].Varint != 42 || fields[2].Varint != 1 || string(fields[3].Bytes) != "hi" {
		t.Fatal(fields)
	}
}
