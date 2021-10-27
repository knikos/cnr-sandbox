package localnetworkrunner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/logging"
)

// interface compliance
var _ network.Network = (*localNetwork)(nil)

// network keeps information uses for network management, and accessing all the nodes
type localNetwork struct {
	log             logging.Logger         // logger used by the library
	binMap          map[nodeType]string    // map node kind to string used in node set up (binary path)
	nextIntNodeID   uint64                 // next id to be used in internal node id generation
	nodes           map[string]*localNode  // access to nodes basic info and api
	coreConfigFlags map[string]interface{} // cmdline flags common to all nodes
	genesis         []byte                 // genesis file contents, common to all nodes
	cChainConfig    []byte                 // cchain config file contents, common to all nodes
}

// NewNetwork creates a network from given configuration and map of node kinds to binaries
func NewNetwork(log logging.Logger, networkConfig network.Config, binMap map[nodeType]string) (network.Network, error) {
	if err := networkConfig.Validate(); err != nil {
		return nil, fmt.Errorf("config failed validation: %w", err)
	}
	net := &localNetwork{
		nodes:         map[string]*localNode{},
		nextIntNodeID: 1,
		binMap:        binMap,
		log:           log,
		genesis:       []byte(networkConfig.Genesis),
		cChainConfig:  []byte(networkConfig.CChainConfig),
	}
	if err := json.Unmarshal([]byte(networkConfig.CoreConfigFlags), &net.coreConfigFlags); err != nil {
		return nil, fmt.Errorf("couldn't unmarshal core config flags: %w", err)
	}
	for _, nodeConfig := range networkConfig.NodeConfigs {
		if _, err := net.AddNode(nodeConfig); err != nil {
			return nil, err
		}
	}
	return net, nil
}

// AddNode prepares the files needed in filesystem by avalanchego, and executes it
func (net *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	var configFlags map[string]interface{} = make(map[string]interface{})

	// get internal node id from incremental uint or from used specification
	nodeID := fmt.Sprint(net.nextIntNodeID)
	net.nextIntNodeID += 1

	if nodeConfig.Name != "" {
		if _, ok := net.nodes[nodeConfig.Name]; ok {
			return nil, fmt.Errorf("repeated node name %s for node %s", nodeConfig.Name, nodeID)
		}
		nodeID = nodeConfig.Name
	}

	if nodeConfig.Type == nil {
		return nil, fmt.Errorf("incomplete node config for node %v: Type field is empty", nodeID)
	}

	if nodeConfig.ConfigFlags == "" {
		return nil, fmt.Errorf("incomplete node config for node %v: ConfigFlags field is empty", nodeID)
	}

	// copy common config flags, and unmarshall specific node config flags
	for k, v := range net.coreConfigFlags {
		configFlags[k] = v
	}
	if err := json.Unmarshal([]byte(nodeConfig.ConfigFlags), &configFlags); err != nil {
		return nil, fmt.Errorf("couldn't unmarshal config flags for node %v: %w", nodeID, err)
	}

	configDir, ok := configFlags[config.ChainConfigDirKey].(string)
	if !ok {
		return nil, fmt.Errorf("node %v lacks config flag %s", nodeID, config.ChainConfigDirKey)
	}

	// create main config file by marshalling all config flags
	configFilePath := path.Join(configDir, "config.json")
	configBytes, err := json.Marshal(configFlags)
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal full config flags for node %v: %w", nodeID, err)
	}
	if err := createFile(configFilePath, configBytes); err != nil {
		return nil, fmt.Errorf("couldn't create config file %s for node %v: %w", configFilePath, nodeID, err)
	}

	cConfigFilePath := path.Join(configDir, "C", "config.json")
	if err := createFile(cConfigFilePath, net.cChainConfig); err != nil {
		return nil, fmt.Errorf("couldn't create cchain config file %s for node %v: %w", cConfigFilePath, nodeID, err)
	}

	genesisFname, ok := configFlags[config.GenesisConfigFileKey].(string)
	if !ok {
		return nil, fmt.Errorf("node %v lacks config flag %s", nodeID, config.GenesisConfigFileKey)
	}
	if err := createFile(genesisFname, net.genesis); err != nil {
		return nil, fmt.Errorf("couldn't create genesis file %s for node %v: %w", genesisFname, nodeID, err)
	}

	// tells if avalanchego is going to generate the cert/key for the node
	usesEphemeralCert, ok := configFlags[config.StakingEphemeralCertEnabledKey].(bool)
	if !ok {
		usesEphemeralCert = false
	}

	if !usesEphemeralCert {
		// cert/key is given in config
		if nodeConfig.Cert == "" {
			return nil, fmt.Errorf("incomplete node config for node %v: Cert field is empty", nodeID)
		}
		certFname, ok := configFlags[config.StakingCertPathKey].(string)
		if !ok {
			return nil, fmt.Errorf("node %v lacks config flag %s", nodeID, config.StakingCertPathKey)
		}
		if err := createFile(certFname, []byte(nodeConfig.Cert)); err != nil {
			return nil, fmt.Errorf("couldn't create cert file %s for node %v: %w", certFname, nodeID, err)
		}
		if nodeConfig.PrivateKey == "" {
			return nil, fmt.Errorf("incomplete node config for node %v: PrivateKey field is empty", nodeID)
		}
		keyFname, ok := configFlags[config.StakingKeyPathKey].(string)
		if !ok {
			return nil, fmt.Errorf("node %v lacks config flag %s", nodeID, config.StakingKeyPathKey)
		}
		if err := createFile(keyFname, []byte(nodeConfig.PrivateKey)); err != nil {
			return nil, fmt.Errorf("couldn't create private key file %s for node %v: %w", keyFname, nodeID, err)
		}
	}

	// initialize node avalanchego apis
	nodeIP, ok := configFlags[config.PublicIPKey].(string)
	if !ok {
		return nil, fmt.Errorf("node %v lacks config flag %s", nodeID, config.PublicIPKey)
	}
	nodePortF, ok := configFlags[config.HTTPPortKey].(float64)
	if !ok {
		return nil, fmt.Errorf("node %v lacks config flag %s", nodeID, config.HTTPPortKey)
	}
	nodePort := uint(nodePortF)
	apiClient := NewAPIClient(nodeIP, nodePort, 20*time.Second)

	// get binary from bin map and node kind, and execute it
	binMapKey := nodeType(nodeConfig.Type.(float64))
	avalanchegoPath, ok := net.binMap[binMapKey]
	if !ok {
		return nil, fmt.Errorf("could not found key %v in binMap for node %v", binMapKey, nodeID)
	}
	configFileFlag := fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath)
	cmd := exec.Command(avalanchegoPath, configFileFlag)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not execute cmd \"%s %s\" for node %v: %w", avalanchegoPath, configFileFlag, nodeID, err)
	}

	node := &localNode{
		id:     nodeID,
		client: apiClient,
		cmd:    cmd,
	}
	net.nodes[nodeID] = node
	return node, nil
}

// Ready closes readyCh is the network has initialized, or send error indicating network will not initialize
func (net *localNetwork) Ready() (chan struct{}, chan error) {
	readyCh := make(chan struct{})
	errorCh := make(chan error)
	go func() {
		for id := range net.nodes {
			b := waitNode(net.nodes[id].client)
			if !b {
				errorCh <- fmt.Errorf("timeout waiting for node %v", id)
			}
			net.log.Info("node %v is up", id)
		}
		close(readyCh)
	}()
	return readyCh, errorCh
}

func (net *localNetwork) GetNode(nodeID string) (node.Node, error) {
	node, ok := net.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not found in network", nodeID)
	}
	return node, nil
}

func (net *localNetwork) GetNodesIDs() []string {
	ks := make([]string, 0, len(net.nodes))
	for k := range net.nodes {
		ks = append(ks, k)
	}
	return ks
}

func (net *localNetwork) Stop() error {
	for nodeID := range net.nodes {
		if err := net.RemoveNode(nodeID); err != nil {
			return err
		}
	}
	return nil
}

func (net *localNetwork) RemoveNode(nodeID string) error {
	node, ok := net.nodes[nodeID]
	if !ok {
		return fmt.Errorf("node %s not found in network", nodeID)
	}
	delete(net.nodes, nodeID)
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	if err := node.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("error sending SIGTERM to %s: %w", node.nodeID, err)
	}
	return nil
}

// createFile creates a file under the fiven fname, making all
// intermediate dirs, and filling it with contents
func createFile(fname string, contents []byte) error {
	if err := os.MkdirAll(path.Dir(fname), 0o750); err != nil {
		return err
	}
	file, err := os.Create(fname)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		return err
	}
	file.Close()
	return nil
}

// waitNode waits until the node initializes, or timeouts
func waitNode(client api.Client) bool {
	info := client.InfoAPI()
	timeout := 1 * time.Minute
	pollTime := 10 * time.Second
	nodeIsUp := false
	for t0 := time.Now(); !nodeIsUp && time.Since(t0) <= timeout; {
		nodeIsUp = true
		if bootstrapped, err := info.IsBootstrapped("P"); err != nil || !bootstrapped {
			nodeIsUp = false
			continue
		}
		if bootstrapped, err := info.IsBootstrapped("C"); err != nil || !bootstrapped {
			nodeIsUp = false
			continue
		}
		if bootstrapped, err := info.IsBootstrapped("X"); err != nil || !bootstrapped {
			nodeIsUp = false
		}
		if !nodeIsUp {
			time.Sleep(pollTime)
		}
	}
	return nodeIsUp
}