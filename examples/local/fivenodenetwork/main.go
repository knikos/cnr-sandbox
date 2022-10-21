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

package main

import (
	"context"
	"fmt"
	"go/build"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chain4travel/camino-network-runner/local"
	"github.com/chain4travel/camino-network-runner/network"
	"github.com/chain4travel/caminogo/utils/logging"
)

const (
	healthyTimeout = 2 * time.Minute
)

var goPath = os.ExpandEnv("$GOPATH")

// Blocks until a signal is received on [signalChan], upon which
// [n.Stop()] is called. If [signalChan] is closed, does nothing.
// Closes [closedOnShutdownChan] amd [signalChan] when done shutting down network.
// This function should only be called once.
func shutdownOnSignal(
	log logging.Logger,
	n network.Network,
	signalChan chan os.Signal,
	closedOnShutdownChan chan struct{},
) {
	sig := <-signalChan
	log.Info("got OS signal %s", sig)
	if err := n.Stop(context.Background()); err != nil {
		log.Debug("error while stopping network: %s", err)
	}
	signal.Reset()
	close(signalChan)
	close(closedOnShutdownChan)
}

// Shows example usage of the Camino Network Runner.
// Creates a local five node Camino network
// and waits for all nodes to become healthy.
// The network runs until the user provides a SIGINT or SIGTERM.
func main() {
	// Create the logger
	logFactory := logging.NewFactory(logging.DefaultConfig)
	log, err := logFactory.Make("main")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if goPath == "" {
		goPath = build.Default.GOPATH
	}

	var binaryPath string
	if len(os.Args) == 2 && os.Args[1] != "" {
		binaryPath = os.Args[1]
	} else {
		binaryPath = fmt.Sprintf("%s%s", goPath, "/src/github.com/chain4travel/caminogo/build/caminogo")
	}

	if err := run(log, binaryPath); err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
}

func run(log logging.Logger, binaryPath string) error {
	// Create the network
	nw, err := local.NewDefaultNetwork(log, binaryPath)
	if err != nil {
		return err
	}
	defer func() { // Stop the network when this function returns
		if err := nw.Stop(context.Background()); err != nil {
			log.Debug("error stopping network: %w", err)
		}
	}()

	// When we get a SIGINT or SIGTERM, stop the network and close [closedOnShutdownCh]
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT)
	signal.Notify(signalsChan, syscall.SIGTERM)
	closedOnShutdownCh := make(chan struct{})
	go func() {
		shutdownOnSignal(log, nw, signalsChan, closedOnShutdownCh)
	}()

	// Wait until the nodes in the network are ready
	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	healthyChan := nw.Healthy(ctx)
	log.Info("waiting for all nodes to report healthy...")
	if err := <-healthyChan; err != nil {
		return err
	}

	log.Info("All nodes healthy. Network will run until you CTRL + C to exit...")
	// Wait until done shutting down network after SIGINT/SIGTERM
	<-closedOnShutdownCh
	return nil
}
