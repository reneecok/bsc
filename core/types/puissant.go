package types

import (
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/shopspring/decimal"
	"math/big"
	"sort"
)

type PuissantPackage struct {
	id           PuissantID
	txs          Transactions
	maxTimestamp uint64
	bidGasPrice  *big.Int // gas price of the first transaction
}

func NewPuissantPackage(pid PuissantID, txs Transactions, maxTS uint64) *PuissantPackage {
	return &PuissantPackage{
		id:           pid,
		txs:          txs,
		maxTimestamp: maxTS,
		bidGasPrice:  txs[0].GasPrice(),
	}
}

func (pp *PuissantPackage) ID() PuissantID {
	return pp.id
}

func (pp *PuissantPackage) ExpireAt() uint64 {
	return pp.maxTimestamp
}

func (pp *PuissantPackage) Txs() Transactions {
	return pp.txs
}

func (pp *PuissantPackage) TxCount() int {
	return len(pp.txs)
}

func (pp *PuissantPackage) HigherBidGasPrice(with *PuissantPackage) bool {
	return pp.bidGasPrice.Cmp(with.bidGasPrice) > 0
}

func (pp *PuissantPackage) ReplacedByNewPuissant(np *PuissantPackage, priceBump uint64) bool {
	oldP := new(big.Int).Mul(pp.bidGasPrice, big.NewInt(100+int64(priceBump)))
	newP := new(big.Int).Mul(np.bidGasPrice, big.NewInt(100))

	return newP.Cmp(oldP) > 0
}

func (pp *PuissantPackage) HigherBidGasPriceIntCmp(with *big.Int) bool {
	return pp.bidGasPrice.Cmp(with) > 0
}

func (pp *PuissantPackage) BidGasPrice() *big.Int {
	return new(big.Int).Set(pp.bidGasPrice)
}

// PuissantPackages list of PuissantPackage
type PuissantPackages []*PuissantPackage

func (p PuissantPackages) Len() int {
	return len(p)
}

func (p PuissantPackages) Less(i, j int) bool {
	return p[i].HigherBidGasPrice(p[j])
}

func (p PuissantPackages) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

// transactions queue
type puissantTxQueue Transactions

func (s puissantTxQueue) Len() int { return len(s) }
func (s puissantTxQueue) Less(i, j int) bool {

	bundleSort := func(txI, txJ *Transaction) bool {
		_, txIBSeq, txIInnerSeq := txI.PuissantInfo()
		_, txJBSeq, txJInnerSeq := txJ.PuissantInfo()

		if txIBSeq == txJBSeq {
			return txIInnerSeq < txJInnerSeq
		}
		return txIBSeq < txJBSeq
	}

	cmp := s[i].GasPrice().Cmp(s[j].GasPrice())
	if cmp == 0 {
		iIsBundle := s[i].IsPuissant()
		jIsBundle := s[j].IsPuissant()

		if !iIsBundle && !jIsBundle {
			return s[i].Time().Before(s[j].Time())

		} else if iIsBundle && jIsBundle {
			return bundleSort(s[i], s[j])

		} else if iIsBundle {
			return true

		} else if jIsBundle {
			return false

		}
	}
	return cmp > 0
}

func (s puissantTxQueue) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

type TransactionsPuissant struct {
	txs                map[common.Address]Transactions
	txHeadsAndPuissant puissantTxQueue
	signer             Signer
	enabled            mapset.Set[PuissantID]
}

func NewTransactionsPuissant(signer Signer, txs map[common.Address]Transactions, packages PuissantPackages) *TransactionsPuissant {
	headsAndBundleTxs := make(puissantTxQueue, 0, len(txs))
	for from, accTxs := range txs {
		// Ensure the sender address is from the signer
		if acc, _ := Sender(signer, accTxs[0]); acc != from {
			delete(txs, from)
			continue
		}
		headsAndBundleTxs = append(headsAndBundleTxs, accTxs[0])
		txs[from] = accTxs[1:]
	}

	for _, each := range packages {
		for _, tx := range each.Txs() {
			headsAndBundleTxs = append(headsAndBundleTxs, tx)
		}
	}

	sort.Sort(&headsAndBundleTxs)
	return &TransactionsPuissant{
		enabled:            mapset.NewThreadUnsafeSet[PuissantID](),
		txs:                txs,
		txHeadsAndPuissant: headsAndBundleTxs,
		signer:             signer,
	}
}

func (t *TransactionsPuissant) ResetEnable(pids []PuissantID) {
	t.enabled.Clear()
	for _, pid := range pids {
		t.enabled.Add(pid)
	}
}

func (t *TransactionsPuissant) Copy() *TransactionsPuissant {
	if t == nil {
		return nil
	}

	newHeadsAndBundleTxs := make([]*Transaction, len(t.txHeadsAndPuissant))
	copy(newHeadsAndBundleTxs, t.txHeadsAndPuissant)
	txs := make(map[common.Address]Transactions, len(t.txs))
	for acc, txsTmp := range t.txs {
		txs[acc] = txsTmp
	}
	return &TransactionsPuissant{txHeadsAndPuissant: newHeadsAndBundleTxs, txs: txs, signer: t.signer, enabled: t.enabled.Clone()}
}

func (t *TransactionsPuissant) LogPuissantTxs() {
	for _, tx := range t.txHeadsAndPuissant {
		if tx.IsPuissant() {
			_, pSeq, bInnerSeq := tx.PuissantInfo()
			log.Info("puissant-tx", "seq", fmt.Sprintf("%2d - %d", pSeq, bInnerSeq), "hash", tx.Hash(), "revert", tx.AcceptsReverting(), "gp", tx.GasPrice().Uint64())
		}
	}
}

func (t *TransactionsPuissant) Peek() *Transaction {
	if len(t.txHeadsAndPuissant) == 0 {
		return nil
	}
	next := t.txHeadsAndPuissant[0]
	if pid := next.PuissantID(); pid.IsPuissant() && !t.enabled.Contains(pid) {
		t.Pop()
		return t.Peek()
	}
	return next
}

func (t *TransactionsPuissant) Shift() {
	acc, _ := Sender(t.signer, t.txHeadsAndPuissant[0])
	if !t.txHeadsAndPuissant[0].IsPuissant() {
		if txs, ok := t.txs[acc]; ok && len(txs) > 0 {
			t.txHeadsAndPuissant[0], t.txs[acc] = txs[0], txs[1:]
			sort.Sort(&t.txHeadsAndPuissant)
			return
		}
	}
	t.Pop()
}

// Pop removes the best transaction, *not* replacing it with the next one from
// the same account. This should be used when a transaction cannot be executed
// and hence all subsequent ones should be discarded from the same account.
func (t *TransactionsPuissant) Pop() {
	if len(t.txHeadsAndPuissant) > 0 {
		t.txHeadsAndPuissant = t.txHeadsAndPuissant[1:]
	}
}

func WeiToEther(value *big.Int) float64 {
	if value == nil {
		return 0
	}
	res, _ := weiToEtherDecimal(value).Float64()
	return res
}

func weiToEtherDecimal(value *big.Int) decimal.Decimal {
	return decimal.NewFromBigInt(value, 0).Div(decimal.New(1, int32(18)))
}