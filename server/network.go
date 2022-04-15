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

// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chain4travel/camino-network-runner/api"
	"github.com/chain4travel/camino-network-runner/local"
	"github.com/chain4travel/camino-network-runner/network"
	"github.com/chain4travel/camino-network-runner/pkg/color"
	"github.com/chain4travel/camino-network-runner/rpcpb"
	"github.com/chain4travel/caminogo/network/peer"
	"github.com/chain4travel/caminogo/utils/constants"
	"github.com/chain4travel/caminogo/utils/logging"
)

type localNetwork struct {
	logger logging.Logger

	binPath string
	cfg     network.Config

	nw network.Network

	nodeNames []string
	nodeInfos map[string]*rpcpb.NodeInfo

	// maps from node name to peer ID to peer object
	attachedPeers map[string]map[string]peer.Peer

	apiClis map[string]api.Client

	readyc          chan struct{} // closed when local network is ready/healthy
	readycCloseOnce sync.Once

	stopc chan struct{}
	donec chan struct{}
	errc  chan error

	stopOnce sync.Once
}

func newNetwork(execPath string, rootDataDir string, numNodes uint32, whitelistedSubnets string, logLevel string) (*localNetwork, error) {
	lcfg := logging.DefaultConfig
	lcfg.Directory = rootDataDir
	logFactory := logging.NewFactory(lcfg)
	logger, err := logFactory.Make("main")
	if err != nil {
		return nil, err
	}

	if logLevel == "" {
		logLevel = "INFO"
	}

	nodeInfos := make(map[string]*rpcpb.NodeInfo)
	cfg, err := local.NewDefaultConfigNNodes(execPath, numNodes)
	if err != nil {
		return nil, err
	}
	nodeNames := make([]string, len(cfg.NodeConfigs))
	for i := range cfg.NodeConfigs {
		nodeName := fmt.Sprintf("node%d", i+1)
		logDir := filepath.Join(rootDataDir, nodeName, "log")
		dbDir := filepath.Join(rootDataDir, nodeName, "db-dir")

		nodeNames[i] = nodeName
		cfg.NodeConfigs[i].Name = nodeName

		// need to whitelist subnet ID to create custom VM chain
		// ref. vms/platformvm/createChain
		cfg.NodeConfigs[i].ConfigFile = fmt.Sprintf(`{
	"network-peer-list-gossip-frequency":"250ms",
	"network-max-reconnect-delay":"1s",
	"public-ip":"127.0.0.1",
	"health-check-frequency":"2s",
	"api-admin-enabled":true,
	"api-ipcs-enabled":true,
	"index-enabled":true,
	"log-display-level":"INFO",
	"log-level":"%s",
	"log-dir":"%s",
	"db-dir":"%s",
	"whitelisted-subnets":"%s"
}`,
			strings.ToUpper(logLevel),
			logDir,
			dbDir,
			whitelistedSubnets,
		)
		cfg.NodeConfigs[i].ImplSpecificConfig = json.RawMessage(fmt.Sprintf(`{"binaryPath":"%s","redirectStdout":true,"redirectStderr":true}`, execPath))

		nodeInfos[nodeName] = &rpcpb.NodeInfo{
			Name:               nodeName,
			ExecPath:           execPath,
			Uri:                "",
			Id:                 "",
			LogDir:             logDir,
			DbDir:              dbDir,
			WhitelistedSubnets: whitelistedSubnets,
			Config:             []byte(cfg.NodeConfigs[i].ConfigFile),
		}
	}

	return &localNetwork{
		logger: logger,

		binPath: execPath,
		cfg:     cfg,

		nodeNames:     nodeNames,
		nodeInfos:     nodeInfos,
		apiClis:       make(map[string]api.Client),
		attachedPeers: make(map[string]map[string]peer.Peer),

		readyc: make(chan struct{}),

		stopc: make(chan struct{}),
		donec: make(chan struct{}),
		errc:  make(chan error, 1),
	}, nil
}

func (lc *localNetwork) start() {
	defer func() {
		close(lc.donec)
	}()

	color.Outf("{{blue}}{{bold}}create and run local network{{/}}\n")
	nw, err := local.NewNetwork(lc.logger, lc.cfg, os.TempDir())
	if err != nil {
		lc.errc <- err
		return
	}
	lc.nw = nw

	if err := lc.waitForHealthy(); err != nil {
		lc.errc <- err
		return
	}
}

const healthyWait = 2 * time.Minute

var errAborted = errors.New("aborted")

func (lc *localNetwork) waitForHealthy() error {
	color.Outf("{{blue}}{{bold}}waiting for all nodes to report healthy...{{/}}\n")

	ctx, cancel := context.WithTimeout(context.Background(), healthyWait)
	defer cancel()
	hc := lc.nw.Healthy(ctx)
	select {
	case <-lc.stopc:
		return errAborted
	case <-ctx.Done():
		return ctx.Err()
	case err := <-hc:
		if err != nil {
			return err
		}
	}

	nodes, err := lc.nw.GetAllNodes()
	if err != nil {
		return err
	}
	for name, node := range nodes {
		uri := fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort())
		nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)

		lc.nodeInfos[name].Uri = uri
		lc.nodeInfos[name].Id = nodeID

		lc.apiClis[name] = node.GetAPIClient()
		color.Outf("{{cyan}}%s: node ID %q, URI %q{{/}}\n", name, nodeID, uri)
	}

	lc.readycCloseOnce.Do(func() {
		close(lc.readyc)
	})
	return nil
}

func (lc *localNetwork) stop() {
	lc.stopOnce.Do(func() {
		close(lc.stopc)
		serr := lc.nw.Stop(context.Background())
		<-lc.donec
		color.Outf("{{red}}{{bold}}terminated network{{/}} (error %v)\n", serr)
	})
}
