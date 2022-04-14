// Copyright (C) 2022, Chain4Travel AG. All rights reserved.
//
// This file is a derived work, based on ava-labs code whose
// original notices appear below.
//
// It is distributed under the same license conditions as the
// original code from which it is derived.
//
// Much love to the original authors for their work.
// **********************************************************

package api

import (
	"github.com/chain4travel/caminogo/api/admin"
	"github.com/chain4travel/caminogo/api/health"
	"github.com/chain4travel/caminogo/api/info"
	"github.com/chain4travel/caminogo/api/ipcs"
	"github.com/chain4travel/caminogo/api/keystore"
	"github.com/chain4travel/caminogo/indexer"
	"github.com/chain4travel/caminogo/vms/avm"
	"github.com/chain4travel/caminogo/vms/platformvm"
	"github.com/ava-labs/coreth/plugin/evm"
)

// Issues API calls to a node
// TODO: byzantine api. check if appropiate. improve implementation.
type Client interface {
	PChainAPI() platformvm.Client
	XChainAPI() avm.Client
	XChainWalletAPI() avm.WalletClient
	CChainAPI() evm.Client
	CChainEthAPI() EthClient // ethclient websocket wrapper that adds mutexed calls, and lazy conn init (on first call)
	InfoAPI() info.Client
	HealthAPI() health.Client
	IpcsAPI() ipcs.Client
	KeystoreAPI() keystore.Client
	AdminAPI() admin.Client
	PChainIndexAPI() indexer.Client
	CChainIndexAPI() indexer.Client
	// TODO add methods
}
