// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package state

import (
	"bytes"

	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/log"
	"github.com/ledgerwatch/turbo-geth/trie"
	"github.com/petar/GoLLRB/llrb"
)

type storageItem struct {
	key, seckey, value common.Hash
}

func (a *storageItem) Less(b llrb.Item) bool {
	bi := b.(*storageItem)
	return bytes.Compare(a.seckey[:], bi.seckey[:]) < 0
}

// Implements StateReader by wrapping database only, without trie
type DbState struct {
	db      ethdb.Getter
	blockNr uint64
	storage map[common.Address]*llrb.LLRB
}

func NewDbState(db ethdb.Getter, blockNr uint64) *DbState {
	return &DbState{
		db:      db,
		blockNr: blockNr,
		storage: make(map[common.Address]*llrb.LLRB),
	}
}

func (dbs *DbState) SetBlockNr(blockNr uint64) {
	dbs.blockNr = blockNr
}

func (dbs *DbState) ForEachStorage(addr common.Address, start []byte, cb func(key, seckey, value common.Hash) bool, maxResults int) {
	st := llrb.New()
	var s [20 + 32]byte
	copy(s[:], addr[:])
	copy(s[20:], start)
	var lastSecKey common.Hash
	overrideCounter := 0
	emptyHash := common.Hash{}
	min := &storageItem{seckey: common.BytesToHash(start)}
	if t, ok := dbs.storage[addr]; ok {
		t.AscendGreaterOrEqual(min, func(i llrb.Item) bool {
			item := i.(*storageItem)
			st.ReplaceOrInsert(item)
			if item.value != emptyHash {
				copy(lastSecKey[:], item.seckey[:])
				// Only count non-zero items
				overrideCounter++
			}
			return overrideCounter < maxResults
		})
	}
	numDeletes := st.Len() - overrideCounter
	dbs.db.WalkAsOf(StorageBucket, StorageHistoryBucket, s[:], 0, dbs.blockNr+1, func(ks, vs []byte) (bool, error) {
		if !bytes.HasPrefix(ks, addr[:]) {
			return false, nil
		}
		if vs == nil || len(vs) == 0 {
			// Skip deleted entries
			return true, nil
		}
		seckey := ks[20:]
		//fmt.Printf("seckey: %x\n", seckey)
		si := storageItem{}
		copy(si.seckey[:], seckey)
		if st.Has(&si) {
			return true, nil
		}
		si.value.SetBytes(vs)
		st.InsertNoReplace(&si)
		if bytes.Compare(seckey[:], lastSecKey[:]) > 0 {
			// Beyond overrides
			return st.Len() < maxResults+numDeletes, nil
		}
		return st.Len() < maxResults+overrideCounter+numDeletes, nil
	})
	results := 0
	st.AscendGreaterOrEqual(min, func(i llrb.Item) bool {
		item := i.(*storageItem)
		if item.value != emptyHash {
			// Skip if value == 0
			if item.key == emptyHash {
				key, err := dbs.db.Get(trie.SecureKeyPrefix, item.seckey[:])
				if err == nil {
					copy(item.key[:], key)
				} else {
					log.Error("Error getting preimage", "err", err)
				}
			}
			cb(item.key, item.seckey, item.value)
			results++
		}
		return results < maxResults
	})
}

func (dbs *DbState) ReadAccountData(address common.Address) (*Account, error) {
	h := newHasher()
	defer returnHasherToPool(h)
	h.sha.Reset()
	h.sha.Write(address[:])
	var buf common.Hash
	h.sha.Read(buf[:])
	enc, err := dbs.db.GetAsOf(AccountsBucket, AccountsHistoryBucket, buf[:], dbs.blockNr+1)
	if err != nil || enc == nil || len(enc) == 0 {
		return nil, nil
	}
	return encodingToAccount(enc)
}

func (dbs *DbState) ReadAccountStorage(address common.Address, key *common.Hash) ([]byte, error) {
	h := newHasher()
	defer returnHasherToPool(h)
	h.sha.Reset()
	h.sha.Write(key[:])
	var buf common.Hash
	h.sha.Read(buf[:])
	enc, err := dbs.db.GetAsOf(StorageBucket, StorageHistoryBucket, append(address[:], buf[:]...), dbs.blockNr+1)
	if err != nil || enc == nil {
		return nil, nil
	}
	return enc, nil
}

func (dbs *DbState) ReadAccountCode(codeHash common.Hash) ([]byte, error) {
	if bytes.Equal(codeHash[:], emptyCodeHash) {
		return nil, nil
	}
	return dbs.db.Get(CodeBucket, codeHash[:])
}

func (dbs *DbState) ReadAccountCodeSize(codeHash common.Hash) (int, error) {
	code, err := dbs.ReadAccountCode(codeHash)
	if err != nil {
		return 0, err
	}
	return len(code), nil
}

func (dbs *DbState) UpdateAccountData(address common.Address, original, account *Account) error {
	return nil
}

func (dbs *DbState) DeleteAccount(address common.Address, original *Account) error {
	return nil
}

func (dbs *DbState) UpdateAccountCode(codeHash common.Hash, code []byte) error {
	return nil
}

func (dbs *DbState) WriteAccountStorage(address common.Address, key, original, value *common.Hash) error {
	t, ok := dbs.storage[address]
	if !ok {
		t = llrb.New()
		dbs.storage[address] = t
	}
	h := newHasher()
	defer returnHasherToPool(h)
	h.sha.Reset()
	h.sha.Write(key[:])
	i := &storageItem{key: *key, value: *value}
	h.sha.Read(i.seckey[:])
	t.ReplaceOrInsert(i)
	return nil
}
