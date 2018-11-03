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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pasl-project/pasl/api"
	"github.com/pasl-project/pasl/blockchain"
	"github.com/pasl-project/pasl/crypto"
	"github.com/pasl-project/pasl/defaults"
	"github.com/pasl-project/pasl/network"
	"github.com/pasl-project/pasl/network/pasl"
	"github.com/pasl-project/pasl/storage"
	"github.com/pasl-project/pasl/utils"

	"github.com/modern-go/concurrent"
)

func main() {
	defer utils.TimeTrack(time.Now(), "Terminated, %s elapsed")

	utils.Tracef(defaults.UserAgent)

	var p2pPort uint
	flag.UintVar(&p2pPort, "p2p-bind-port", uint(defaults.P2PPort), "P2P bind port")
	var dataDir string
	flag.StringVar(&dataDir, "data-dir", "", "Directory to store blockchain files")
	flag.Parse()

	if dataDir == "" {
		var err error
		if dataDir, err = utils.GetDataDir(); err != nil {
			utils.Panicf("Failed to obtain valid data directory path. Use --data-dir to manually specify data directory location. Error: %v", err)
		}
	}

	if err := utils.CreateDirectory(&dataDir); err != nil {
		utils.Panicf("Failed to create data directory %v", err)
	}
	dbFileName := filepath.Join(dataDir, "storage.db")
	err := storage.WithStorage(&dbFileName, defaults.AccountsPerBlock, func(storage storage.Storage) error {
		blockchain, err := blockchain.NewBlockchain(storage)
		if err != nil {
			return err
		}

		config := network.Config{
			ListenAddr:     fmt.Sprintf("%s:%d", defaults.P2PBindAddress, p2pPort),
			MaxIncoming:    defaults.MaxIncoming,
			MaxOutgoing:    defaults.MaxOutgoing,
			TimeoutConnect: defaults.TimeoutConnect,
		}

		key, err := crypto.NewKey(crypto.NIDsecp256k1)
		if err != nil {
			return err
		}
		nonce := utils.Serialize(key.Public)

		peerUpdates := make(chan pasl.PeerInfo)
		return pasl.WithManager(nonce, blockchain, peerUpdates, defaults.TimeoutRequest, func(manager network.Manager) error {
			return network.WithNode(config, manager, func(node network.Node) error {
				for _, hostPort := range strings.Split(defaults.BootstrapNodes, ",") {
					node.AddPeer("tcp", hostPort)
				}

				updatesListener := concurrent.NewUnboundedExecutor()
				updatesListener.Go(func(ctx context.Context) {
					for {
						select {
						case peer := <-peerUpdates:
							utils.Tracef("   %s:%d last seen %s ago", peer.Host, peer.Port, time.Since(time.Unix(int64(peer.LastConnect), 0)))
							node.AddPeer("tcp", fmt.Sprintf("%s:%d", peer.Host, peer.Port))
						case <-ctx.Done():
							return
						}
					}
				})
				defer updatesListener.StopAndWaitForever()

				return network.WithRpcServer(fmt.Sprintf("%s:%d", defaults.RPCBindAddress, defaults.RPCPort), api.NewApi(blockchain), func() error {
					c := make(chan os.Signal, 2)
					signal.Notify(c, os.Interrupt, syscall.SIGTERM)
					<-c
					utils.Tracef("Exit signal received. Terminating...")
					return nil
				})
			})
		})
	})
	if err != nil {
		utils.Panicf("Failed to initialize storage. %v", err)
	}
}
