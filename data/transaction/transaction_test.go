package transaction_test

import (
	"fmt"
	"math/rand"
	"testing"

	"encoding/json"
	"reflect"

	"github.com/ElrondNetwork/elrond-go-sandbox/data/transaction"
	"zombiezen.com/go/capnproto2"
)

type Serializer interface {
	Marshal(obj interface{}) []byte
	Unmarshal(input []byte, obj interface{}) error
}

type Tx struct {
	Nonce     []byte
	Value     []byte
	RcvAddr   []byte
	SndAddr   []byte
	GasPrice  []byte
	GasLimit  []byte
	Data      []byte
	Signature []byte
	Challenge []byte
	PubKey    []byte
}

func newTransaction(tx *transaction.Transaction, a *Tx) {
	tx.SetRcvAddr(a.RcvAddr)
	tx.SetSndAddr(a.SndAddr)
	tx.SetPubKey(a.PubKey)
	tx.SetGasPrice(a.GasPrice)
	tx.SetGasLimit(a.GasLimit)
	tx.SetNonce(a.Nonce)
	tx.SetValue(a.Value)
	tx.SetData(a.Data)
	tx.SetChallenge(a.Challenge)
	tx.SetSignature(a.Signature)
}

type CapnpSerializer struct {
	arena capnp.Arena
}

type JsonSerializer struct {
}

func BenchmarkPopulateCapnp(b *testing.B) {
	txs := generateDummyTxs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, segment, _ := capnp.NewMessage(capnp.SingleSegment(nil))
		record, _ := transaction.NewRootTransaction(segment)
		newTransaction(&record, txs[i%1000])
	}
}

func (x *CapnpSerializer) Marshal(obj interface{}) []byte {
	txObj := obj.(*Tx)

	m, s, _ := capnp.NewMessage(x.arena)
	tx, _ := transaction.NewRootTransaction(s)
	newTransaction(&tx, txObj)
	b, _ := m.Marshal()
	return b
}

func (x *CapnpSerializer) Unmarshal(d []byte, obj interface{}) error {
	txObj := obj.(*Tx)

	m, _ := capnp.Unmarshal(d)
	tx, _ := transaction.ReadRootTransaction(m)

	txObj.Nonce, _ = tx.Nonce()
	txObj.Value, _ = tx.Value()
	txObj.RcvAddr, _ = tx.RcvAddr()
	txObj.SndAddr, _ = tx.SndAddr()
	txObj.GasPrice, _ = tx.GasPrice()
	txObj.GasLimit, _ = tx.GasLimit()
	txObj.Data, _ = tx.Data()
	txObj.Signature, _ = tx.Signature()
	txObj.Challenge, _ = tx.Challenge()
	txObj.PubKey, _ = tx.PubKey()

	return nil
}

func (x *JsonSerializer) Marshal(obj interface{}) []byte {
	d, _ := json.Marshal(obj)
	return d
}

func (x *JsonSerializer) Unmarshal(d []byte, obj interface{}) error {
	return json.Unmarshal(d, obj)
}

func randomStr(l int) string {
	buf := make([]byte, l)

	for i := 0; i < (l+1)/2; i++ {
		buf[i] = byte(rand.Intn(256))
	}
	return fmt.Sprintf("%x", buf)[:l]
}

func generateDummyTxs() []*Tx {
	txs := make([]*Tx, 0, 1000)
	for i := 0; i < 1000; i++ {
		txs = append(txs, &Tx{
			Nonce:     []byte(randomStr(4)),
			Value:     []byte(randomStr(5)),
			RcvAddr:   []byte(randomStr(32)),
			SndAddr:   []byte(randomStr(32)),
			GasPrice:  []byte(randomStr(4)),
			GasLimit:  []byte(randomStr(5)),
			Data:      []byte(randomStr(20)),
			Signature: []byte(randomStr(32)),
			Challenge: []byte(randomStr(32)),
			PubKey:    []byte(randomStr(32)),
		})
	}
	return txs
}

func benchMarshal(b *testing.B, s Serializer) {
	b.StopTimer()
	txs := generateDummyTxs()
	l := len(txs)

	b.ReportAllocs()

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		s.Marshal(txs[i%l])
	}
}

func benchUnmarshal(b *testing.B, s Serializer, validate bool) {
	b.StopTimer()
	txs := generateDummyTxs()
	l := len(txs)
	serialized := make([][]byte, l)

	for i, obj := range txs {
		mar := s.Marshal(obj)
		t := make([]byte, len(mar))

		copy(t, mar)
		serialized[i] = t
	}

	b.ReportAllocs()

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		obj := &Tx{}
		n := i % l
		err := s.Unmarshal(serialized[n], obj)

		if err != nil {
			b.Fatalf("%s error unmarshalling %s : %s", s, serialized[n], err)
		}

		// Check unmarshalled data as expected
		if validate {
			orig := txs[n]
			valid := reflect.DeepEqual(orig, obj)
			if !valid {
				b.Fatalf("unmarshaled data different than expected: \n%v\n%v", orig, obj)
			}
		}
	}
}

// benchmarks
func BenchmarkCapnprotoTransactionMarshal(b *testing.B) {
	benchMarshal(b, &CapnpSerializer{capnp.SingleSegment(nil)})
}

func BenchmarkJsonTransactionMarshal(b *testing.B) {
	benchMarshal(b, &JsonSerializer{})
}

func BenchmarkCapnprotoTransactionUnmarshalNoValidate(b *testing.B) {
	benchUnmarshal(b, &CapnpSerializer{capnp.SingleSegment(nil)}, false)
}

func BenchmarkJsonTransactionUnmarshalNoValidate(b *testing.B) {
	benchUnmarshal(b, &JsonSerializer{}, false)
}

func BenchmarkCapnprotoTransactionUnmarshalValidate(b *testing.B) {
	benchUnmarshal(b, &CapnpSerializer{capnp.SingleSegment(nil)}, true)
}

func BenchmarkJsonTransactionUnmarshalValidate(b *testing.B) {
	benchUnmarshal(b, &JsonSerializer{}, true)
}
