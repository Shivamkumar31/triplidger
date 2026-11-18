package ledger

import (
	"errors"
	"sort"
)

const MaxAmount int64 = 100000000000 // Rs 1 billion per expense, below JS safe integer limit.
type Share struct {
	UserID string `json:"user_id"`
	Amount int64  `json:"amount_paise"`
}
type Transfer struct {
	From   string `json:"from_user_id"`
	To     string `json:"to_user_id"`
	Amount int64  `json:"amount_paise"`
}

func Equal(amount int64, ids []string) ([]Share, error) {
	if amount <= 0 || amount > MaxAmount || len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("amount or participants out of range")
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	out := make([]Share, len(ids))
	for i, id := range ids {
		if id == "" || (i > 0 && id == ids[i-1]) {
			return nil, errors.New("duplicate or empty participant")
		}
		a := amount / int64(len(ids))
		if int64(i) < amount%int64(len(ids)) {
			a++
		}
		out[i] = Share{id, a}
	}
	return out, nil
}
func Suggest(balances map[string]int64) []Transfer {
	type part struct {
		id     string
		amount int64
	}
	var debt, credit []part
	ids := make([]string, 0, len(balances))
	for id := range balances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b := balances[id]
		if b < 0 {
			debt = append(debt, part{id, -b})
		} else if b > 0 {
			credit = append(credit, part{id, b})
		}
	}
	out := []Transfer{}
	i, j := 0, 0
	for i < len(debt) && j < len(credit) {
		a := min(debt[i].amount, credit[j].amount)
		out = append(out, Transfer{debt[i].id, credit[j].id, a})
		debt[i].amount -= a
		credit[j].amount -= a
		if debt[i].amount == 0 {
			i++
		}
		if credit[j].amount == 0 {
			j++
		}
	}
	return out
}
