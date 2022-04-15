// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package control

import (
	"context"
	"encoding/base64"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chain4travel/camino-network-runner/client"
	"github.com/chain4travel/camino-network-runner/local"
	"github.com/chain4travel/camino-network-runner/pkg/color"
	"github.com/chain4travel/camino-network-runner/pkg/logutil"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	logLevel       string
	endpoint       string
	dialTimeout    time.Duration
	requestTimeout time.Duration
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control [options]",
		Short: "Start a network runner controller.",
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logutil.DefaultLogLevel, "log level")
	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "0.0.0.0:8080", "server endpoint")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().DurationVar(&requestTimeout, "request-timeout", time.Minute, "client request timeout")

	cmd.AddCommand(
		newStartCommand(),
		newHealthCommand(),
		newURIsCommand(),
		newStatusCommand(),
		newStreamStatusCommand(),
		newRemoveNodeCommand(),
		newRestartNodeCommand(),
		newAttachPeerCommand(),
		newSendOutboundMessageCommand(),
		newStopCommand(),
	)

	return cmd
}

var (
	caminoGoBinPath    string
	whitelistedSubnets string
	numNodes           uint32
)

func newStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [options]",
		Short: "Starts the server.",
		RunE:  startFunc,
	}
	cmd.PersistentFlags().StringVar(
		&caminoGoBinPath,
		"caminogo-path",
		"",
		"caminogo binary path",
	)
	cmd.PersistentFlags().Uint32Var(
		&numNodes,
		"number-of-nodes",
		local.DefaultNumNodes,
		"number of nodes of the network",
	)
	cmd.PersistentFlags().StringVar(
		&whitelistedSubnets,
		"whitelisted-subnets",
		"",
		"whitelisted subnets (comma-separated)",
	)
	return cmd
}

func startFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.Start(ctx, caminoGoBinPath, client.WithNumNodes(numNodes), client.WithWhitelistedSubnets(whitelistedSubnets))
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}start response:{{/}} %+v\n", info)
	return nil
}

func newHealthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [options]",
		Short: "Requests server health.",
		RunE:  healthFunc,
	}
	return cmd
}

func healthFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.Health(ctx)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}health response:{{/}} %+v\n", resp)
	return nil
}

func newURIsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uris [options]",
		Short: "Requests server uris.",
		RunE:  urisFunc,
	}
	return cmd
}

func urisFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	uris, err := cli.URIs(ctx)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}URIs:{{/}} %q\n", uris)
	return nil
}

func newStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [options]",
		Short: "Requests server status.",
		RunE:  statusFunc,
	}
	return cmd
}

func statusFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.Status(ctx)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}status response:{{/}} %+v\n", resp)
	return nil
}

var pushInterval time.Duration

func newStreamStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stream-status [options]",
		Short: "Requests server bootstrap status.",
		RunE:  streamStatusFunc,
	}
	cmd.PersistentFlags().DurationVar(
		&pushInterval,
		"push-interval",
		5*time.Second,
		"interval that server pushes status updates to the client",
	)
	return cmd
}

func streamStatusFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	// poll until the cluster is healthy or os signal
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	donec := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	go func() {
		select {
		case sig := <-sigc:
			zap.L().Warn("received signal", zap.String("signal", sig.String()))
		case <-ctx.Done():
		}
		cancel()
		close(donec)
	}()

	ch, err := cli.StreamStatus(ctx, pushInterval)
	if err != nil {
		return err
	}
	for info := range ch {
		color.Outf("{{cyan}}cluster info:{{/}} %+v\n", info)
	}
	cancel() // receiver channel is closed, so cancel goroutine
	<-donec
	return nil
}

var nodeName string

func newRemoveNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-node [options]",
		Short: "Removes a node.",
		RunE:  removeNodeFunc,
	}
	cmd.PersistentFlags().StringVar(&nodeName, "node-name", "", "node name to remove")
	return cmd
}

func removeNodeFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.RemoveNode(ctx, nodeName)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}remove node response:{{/}} %+v\n", info)
	return nil
}

func newRestartNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart-node [options]",
		Short: "Restarts the server.",
		RunE:  restartNodeFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name to restart",
	)
	cmd.PersistentFlags().StringVar(
		&caminoGoBinPath,
		"caminogo-path",
		"",
		"caminogo binary path",
	)
	cmd.PersistentFlags().StringVar(
		&whitelistedSubnets,
		"whitelisted-subnets",
		"",
		"whitelisted subnets (comma-separated)",
	)
	return cmd
}

func restartNodeFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.RestartNode(ctx, nodeName, caminoGoBinPath, client.WithWhitelistedSubnets(whitelistedSubnets))
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}restart node response:{{/}} %+v\n", info)
	return nil
}

func newAttachPeerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach-peer [options]",
		Short: "Attaches a peer to the node.",
		RunE:  attachPeerFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name to attach a peer to",
	)
	return cmd
}

func attachPeerFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.AttachPeer(ctx, nodeName)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}attach peer response:{{/}} %+v\n", resp)
	return nil
}

var (
	peerID      string
	msgOp       uint32
	msgBytesB64 string
)

func newSendOutboundMessageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send-outbound-message [options]",
		Short: "Sends an outbound message to an attached peer.",
		RunE:  sendOutboundMessageFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name that has an attached peer",
	)
	cmd.PersistentFlags().StringVar(
		&peerID,
		"peer-id",
		"",
		"peer ID to send a message to",
	)
	cmd.PersistentFlags().Uint32Var(
		&msgOp,
		"message-op",
		0,
		"Message operation type",
	)
	cmd.PersistentFlags().StringVar(
		&msgBytesB64,
		"message-bytes-b64",
		"",
		"Message bytes in base64 encoding",
	)
	return cmd
}

func sendOutboundMessageFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	b, err := base64.StdEncoding.DecodeString(msgBytesB64)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.SendOutboundMessage(ctx, nodeName, peerID, msgOp, b)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}send outbound message response:{{/}} %+v\n", resp)
	return nil
}

func newStopCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [options]",
		Short: "Requests server stop.",
		RunE:  stopFunc,
	}
	return cmd
}

func stopFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.Stop(ctx)
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}stop response:{{/}} %+v\n", info)
	return nil
}
