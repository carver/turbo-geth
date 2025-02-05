// Copyright 2018 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package external

import (
	"fmt"
	"math/big"
	"sync"

	"github.com/ledgerwatch/turbo-geth"
	"github.com/ledgerwatch/turbo-geth/accounts"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/hexutil"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/crypto"
	"github.com/ledgerwatch/turbo-geth/event"
	"github.com/ledgerwatch/turbo-geth/internal/ethapi"
	"github.com/ledgerwatch/turbo-geth/log"
	"github.com/ledgerwatch/turbo-geth/rpc"
	"github.com/ledgerwatch/turbo-geth/signer/core"
)

type ExternalBackend struct {
	signers []accounts.Wallet
}

func (eb *ExternalBackend) Wallets() []accounts.Wallet {
	return eb.signers
}

func NewExternalBackend(endpoint string) (*ExternalBackend, error) {
	signer, err := NewExternalSigner(endpoint)
	if err != nil {
		return nil, err
	}
	return &ExternalBackend{
		signers: []accounts.Wallet{signer},
	}, nil
}

func (eb *ExternalBackend) Subscribe(sink chan<- accounts.WalletEvent) event.Subscription {
	return event.NewSubscription(func(quit <-chan struct{}) error {
		<-quit
		return nil
	})
}

// ExternalSigner provides an API to interact with an external signer (clef)
// It proxies request to the external signer while forwarding relevant
// request headers
type ExternalSigner struct {
	client   *rpc.Client
	endpoint string
	status   string
	cacheMu  sync.RWMutex
	cache    []accounts.Account
}

func NewExternalSigner(endpoint string) (*ExternalSigner, error) {
	client, err := rpc.Dial(endpoint)
	if err != nil {
		return nil, err
	}
	extsigner := &ExternalSigner{
		client:   client,
		endpoint: endpoint,
	}
	// Check if reachable
	version, err := extsigner.pingVersion()
	if err != nil {
		return nil, err
	}
	extsigner.status = fmt.Sprintf("ok [version=%v]", version)
	return extsigner, nil
}

func (api *ExternalSigner) URL() accounts.URL {
	return accounts.URL{
		Scheme: "extapi",
		Path:   api.endpoint,
	}
}

func (api *ExternalSigner) Status() (string, error) {
	return api.status, nil
}

func (api *ExternalSigner) Open(passphrase string) error {
	return fmt.Errorf("operation not supported on external signers")
}

func (api *ExternalSigner) Close() error {
	return fmt.Errorf("operation not supported on external signers")
}

func (api *ExternalSigner) Accounts() []accounts.Account {
	var accnts []accounts.Account
	res, err := api.listAccounts()
	if err != nil {
		log.Error("account listing failed", "error", err)
		return accnts
	}
	for _, addr := range res {
		accnts = append(accnts, accounts.Account{
			URL: accounts.URL{
				Scheme: "extapi",
				Path:   api.endpoint,
			},
			Address: addr,
		})
	}
	api.cacheMu.Lock()
	api.cache = accnts
	api.cacheMu.Unlock()
	return accnts
}

func (api *ExternalSigner) Contains(account accounts.Account) bool {
	api.cacheMu.RLock()
	defer api.cacheMu.RUnlock()
	for _, a := range api.cache {
		if a.Address == account.Address && (account.URL == (accounts.URL{}) || account.URL == api.URL()) {
			return true
		}
	}
	return false
}

func (api *ExternalSigner) Derive(path accounts.DerivationPath, pin bool) (accounts.Account, error) {
	return accounts.Account{}, fmt.Errorf("operation not supported on external signers")
}

func (api *ExternalSigner) SelfDerive(base accounts.DerivationPath, chain ethereum.ChainStateReader) {
	log.Error("operation SelfDerive not supported on external signers")
}

func (api *ExternalSigner) signHash(account accounts.Account, hash []byte) ([]byte, error) {
	return []byte{}, fmt.Errorf("operation not supported on external signers")
}

// SignData signs keccak256(data). The mimetype parameter describes the type of data being signed
func (api *ExternalSigner) SignData(account accounts.Account, mimeType string, data []byte) ([]byte, error) {
	// TODO! Replace this with a call to clef SignData with correct mime-type for Clique, once we
	// have that in place
	return api.signHash(account, crypto.Keccak256(data))
}

func (api *ExternalSigner) SignText(account accounts.Account, text []byte) ([]byte, error) {
	return api.signHash(account, accounts.TextHash(text))
}

func (api *ExternalSigner) SignTx(account accounts.Account, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	res := ethapi.SignTransactionResult{}
	to := common.NewMixedcaseAddress(*tx.To())
	data := hexutil.Bytes(tx.Data())
	args := &core.SendTxArgs{
		Data:     &data,
		Nonce:    hexutil.Uint64(tx.Nonce()),
		Value:    hexutil.Big(*tx.Value()),
		Gas:      hexutil.Uint64(tx.Gas()),
		GasPrice: hexutil.Big(*tx.GasPrice()),
		To:       &to,
		From:     common.NewMixedcaseAddress(account.Address),
	}

	if err := api.client.Call(&res, "account_signTransaction", args); err != nil {
		return nil, err
	}
	return res.Tx, nil
}

func (api *ExternalSigner) SignTextWithPassphrase(account accounts.Account, passphrase string, text []byte) ([]byte, error) {
	return []byte{}, fmt.Errorf("passphrase-operations not supported on external signers")
}

func (api *ExternalSigner) SignTxWithPassphrase(account accounts.Account, passphrase string, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	return nil, fmt.Errorf("passphrase-operations not supported on external signers")
}
func (api *ExternalSigner) SignDataWithPassphrase(account accounts.Account, passphrase, mimeType string, data []byte) ([]byte, error) {
	return nil, fmt.Errorf("passphrase-operations not supported on external signers")
}

func (api *ExternalSigner) listAccounts() ([]common.Address, error) {
	var res []common.Address
	if err := api.client.Call(&res, "account_list"); err != nil {
		return nil, err
	}
	return res, nil
}

func (api *ExternalSigner) signCliqueBlock(a common.Address, rlpBlock hexutil.Bytes) (hexutil.Bytes, error) {
	var sig hexutil.Bytes
	if err := api.client.Call(&sig, "account_signData", core.ApplicationClique.Mime, a, rlpBlock); err != nil {
		return nil, err
	}
	if sig[64] != 27 && sig[64] != 28 {
		return nil, fmt.Errorf("invalid Ethereum signature (V is not 27 or 28)")
	}
	sig[64] -= 27 // Transform V from 27/28 to 0/1 for Clique use
	return sig, nil
}

func (api *ExternalSigner) pingVersion() (string, error) {
	var v string
	if err := api.client.Call(&v, "account_version"); err != nil {
		return "", err
	}
	return v, nil
}
