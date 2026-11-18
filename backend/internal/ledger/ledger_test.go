package ledger

import "testing"

func TestRoundingAndInputs(t *testing.T) {
	s, err := Equal(10000, []string{"c", "a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if s[0].Amount != 3334 || s[1].Amount != 3333 || s[2].Amount != 3333 {
		t.Fatal(s)
	}
	for _, ids := range [][]string{{}, {"a", "a"}, {""}} {
		if _, err := Equal(100, ids); err == nil {
			t.Fatal("accepted invalid participants")
		}
	}
	for _, a := range []int64{-1, 0, MaxAmount + 1} {
		if _, err := Equal(a, []string{"a"}); err == nil {
			t.Fatal("accepted invalid amount")
		}
	}
}
func TestSplitInvariant(t *testing.T) {
	for n := 1; n <= 100; n++ {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = string(rune(1000 + i))
		}
		for a := int64(1); a < 1000; a += 17 {
			s, err := Equal(a, ids)
			if err != nil {
				t.Fatal(err)
			}
			var sum int64
			for _, v := range s {
				sum += v.Amount
			}
			if sum != a {
				t.Fatalf("sum %d want %d", sum, a)
			}
		}
	}
}
func TestSuggestedTransfersSettleEveryone(t *testing.T) {
	b := map[string]int64{"shivam": 225000, "aman": 25000, "riya": -75000, "neha": -175000}
	for _, v := range Suggest(b) {
		if v.Amount <= 0 || v.From == v.To {
			t.Fatal(v)
		}
		b[v.From] += v.Amount
		b[v.To] -= v.Amount
	}
	for _, v := range b {
		if v != 0 {
			t.Fatal(b)
		}
	}
}
