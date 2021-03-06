/*
PASL - Personalized Accounts & Secure Ledger

Copyright (C) 2018 PASL Project

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package safebox

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"

	"github.com/pasl-project/pasl/accounter"
	"github.com/pasl-project/pasl/crypto"
	"github.com/pasl-project/pasl/defaults"
	"github.com/pasl-project/pasl/safebox/tx"
)

func TestReward(t *testing.T) {
	if getReward(0) != 500000 {
		t.Fatal()
	}

	if getReward(420479) != 500000 {
		t.Fatal()
	}

	if getReward(420480) != 250000 {
		t.Fatal()
	}

	if getReward(1000000000) != 10000 {
		t.Fatal()
	}
}

func Test(t *testing.T) {
	accounter := accounter.NewAccounter()
	safebox := NewSafebox(accounter)

	miner, err := crypto.NewKeyByType(crypto.NIDsecp256k1)
	if err != nil {
		t.Fatal(err)
	}

	blocks := make(map[uint32]struct{})
	timestamps := make([]uint32, defaults.MaturationHeight)
	for block := 0; block < len(timestamps); block++ {
		timestamp := rand.Uint32()
		_, err = safebox.ProcessOperations(miner.Public, timestamp, []tx.CommonOperation{}, big.NewInt(0))
		if err != nil {
			t.Fatal(err)
		}
		blocks[uint32(block)] = struct{}{}
		timestamps[len(timestamps)-1-block] = timestamp
	}

	updatedPacks := safebox.GetUpdatedPacks()
	if len(updatedPacks) != len(blocks) {
		t.FailNow()
	}
	for _, each := range updatedPacks {
		if _, ok := blocks[each]; !ok {
			t.FailNow()
		}
	}

	if !reflect.DeepEqual(safebox.GetLastTimestamps(10), timestamps[:10]) {
		t.FailNow()
	}
	if !reflect.DeepEqual(safebox.GetLastTimestamps(uint32(len(timestamps))+5), timestamps) {
		t.FailNow()
	}

	if safebox.GetHeight() != uint32(len(blocks)) {
		t.FailNow()
	}

	safebox.Merge()

	if len(safebox.GetUpdatedPacks()) != 0 {
		t.FailNow()
	}

	safebox.Rollback()

	if safebox.GetHeight() != defaults.MaturationHeight {
		t.FailNow()
	}

	{
		transaction := tx.Transfer{
			Source:      defaults.AccountsPerBlock,
			OperationId: 1,
			Destination: 2,
			Amount:      3,
			Fee:         4,
			Payload:     nil,
		}
		_, _, err = tx.Sign(&transaction, miner)
		if err != nil {
			t.Fatal(err)
		}

		height := safebox.GetHeight()

		_, err = safebox.ProcessOperations(miner.Public, 0, []tx.CommonOperation{&transaction}, big.NewInt(0))
		if err == nil {
			t.Fatal(err)
		}

		if safebox.GetHeight() != height {
			t.FailNow()
		}
	}

	{
		const src = 0
		const dest = 2
		const amount = 3
		const fee = 4

		prevBalance := safebox.GetAccount(src).GetBalance()

		transaction := tx.Transfer{
			Source:      src,
			OperationId: 1,
			Destination: dest,
			Amount:      amount,
			Fee:         fee,
			Payload:     nil,
		}

		randomKey, err := crypto.NewKeyByType(crypto.NIDsecp256k1)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = tx.Sign(&transaction, randomKey)
		if err != nil {
			t.Fatal(err)
		}

		height := safebox.GetHeight()

		_, err = safebox.ProcessOperations(miner.Public, 0, []tx.CommonOperation{&transaction}, big.NewInt(0))
		if err == nil {
			t.Fatal(err)
		}

		if safebox.GetHeight() != height {
			t.FailNow()
		}

		_, _, err = tx.Sign(&transaction, miner)
		if err != nil {
			t.Fatal(err)
		}

		height = safebox.GetHeight()

		_, err = safebox.ProcessOperations(miner.Public, 0, []tx.CommonOperation{&transaction}, big.NewInt(0))
		if err != nil {
			t.Fatal(err)
		}

		if safebox.GetHeight() <= height {
			t.FailNow()
		}

		if safebox.GetAccount(0).GetBalance() != prevBalance-amount-fee {
			t.FailNow()
		}
		if safebox.GetAccount(dest).GetBalance() != amount {
			t.FailNow()
		}
	}
}

func TestValidation(t *testing.T) {
	accounter := accounter.NewAccounter()
	safebox := NewSafebox(accounter)

	miner, err := crypto.NewKeyByType(crypto.NIDsecp256k1)
	if err != nil {
		t.Fatal(err)
	}

	for blocks := uint32(0); blocks < defaults.MaturationHeight; blocks++ {
		_, err = safebox.ProcessOperations(miner.Public, 0, []tx.CommonOperation{}, big.NewInt(0))
		if err != nil {
			t.Fatal(err)
		}
	}

	safebox.Merge()

	height := safebox.GetHeight()
	if height != defaults.MaturationHeight {
		t.FailNow()
	}

	const source = uint32(0)

	{
		transaction := tx.Transfer{
			Source:      source,
			OperationId: 1,
			Destination: source,
			Amount:      3,
			Fee:         4,
			Payload:     nil,
		}
		_, _, err = tx.Sign(&transaction, miner)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := safebox.ProcessOperations(nil, 0, []tx.CommonOperation{&transaction}, nil); err == nil {
			t.FailNow()
		}
	}

	// TODO: temporarily disabled, there are some blocks containing such txes
	// serializedPublic := bytes.NewBuffer(nil)
	// if err := miner.Public.Serialize(serializedPublic); err != nil {
	// 	t.Fatal(err)
	// }
	// transaction := tx.ChangeKey{
	// 	Source:       source,
	// 	OperationId:  1,
	// 	Fee:          2,
	// 	Payload:      nil,
	// 	NewPublickey: serializedPublic.Bytes(),
	// }
	// _, _, err = tx.Sign(&transaction, miner.Convert())
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// if _, err := safebox.ProcessOperations(nil, 0, []tx.CommonOperation{&transaction}, nil); err == nil {
	// 	t.FailNow()
	// }

	// if safebox.GetHeight() != height {
	// 	t.FailNow()
	// }

}
