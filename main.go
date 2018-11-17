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
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/modern-go/concurrent"
	"github.com/urfave/cli"

	"github.com/pasl-project/pasl/api"
	"github.com/pasl-project/pasl/blockchain"
	"github.com/pasl-project/pasl/crypto"
	"github.com/pasl-project/pasl/defaults"
	"github.com/pasl-project/pasl/network"
	"github.com/pasl-project/pasl/network/pasl"
	"github.com/pasl-project/pasl/storage"
	"github.com/pasl-project/pasl/utils"
)

func exportMain(ctx *cli.Context) error {
	return cli.ShowSubcommandHelp(ctx)
}

func exportSafebox(ctx *cli.Context) error {
	return withBlockchain(ctx, func(blockchain *blockchain.Blockchain, _ storage.Storage) error {
		blob := blockchain.ExportSafebox()
		fmt.Fprint(ctx.App.Writer, hex.EncodeToString(blob))
		return nil
	})
}

var heightFlagValue uint
var heightFlag = cli.UintFlag{
	Name:        "height",
	Usage:       "Rescan blockchain and recover safebox at specific height",
	Destination: &heightFlagValue,
}
var exportCommand = cli.Command{
	Action:      exportMain,
	Name:        "export",
	Usage:       "Export blockchain data",
	Description: "",
	Subcommands: []cli.Command{
		{
			Action:      exportSafebox,
			Name:        "safebox",
			Usage:       "Export safebox contents",
			Description: "",
			Flags: []cli.Flag{
				heightFlag,
			},
		},
	},
}

func getMain(ctx *cli.Context) error {
	return cli.ShowSubcommandHelp(ctx)
}

func getHeight(ctx *cli.Context) error {
	return withBlockchain(ctx, func(blockchain *blockchain.Blockchain, _ storage.Storage) error {
		height, _, _ := blockchain.GetState()
		fmt.Fprintf(ctx.App.Writer, "%d\n", height)
		return nil
	})
}

var getCommand = cli.Command{
	Action:      getMain,
	Name:        "get",
	Usage:       "Get blockchain info",
	Description: "",
	Subcommands: []cli.Command{
		{
			Action:      getHeight,
			Name:        "height",
			Usage:       "Get current height",
			Description: "",
		},
	},
}

var p2pPortFlag = cli.UintFlag{
	Name:  "p2p-bind-port",
	Usage: "P2P bind port",
	Value: uint(defaults.P2PPort),
}
var dataDirFlag = cli.StringFlag{
	Name:  "data-dir",
	Usage: "Directory to store blockchain files",
}
var exclusiveNodesFlag = cli.StringFlag{
	Name:  "exclusive-nodes",
	Usage: "Comma-separated ip:port list of exclusive nodes to connect to",
}

func withBlockchain(ctx *cli.Context, fn func(blockchain *blockchain.Blockchain, storage storage.Storage) error) error {
	dataDir := ctx.GlobalString(dataDirFlag.GetName())
	if dataDir == "" {
		var err error
		if dataDir, err = utils.GetDataDir(); err != nil {
			return fmt.Errorf("Failed to obtain valid data directory path. Use %s flag to manually specify data directory location. Error: %v", dataDirFlag.GetName(), err)
		}
	}

	if err := utils.CreateDirectory(&dataDir); err != nil {
		return fmt.Errorf("Failed to create data directory %v", err)
	}
	dbFileName := filepath.Join(dataDir, "storage.db")
	err := storage.WithStorage(&dbFileName, func(storage storage.Storage) (err error) {
		var blockchainInstance *blockchain.Blockchain
		if ctx.IsSet(heightFlag.GetName()) {
			var height uint32
			height = uint32(heightFlagValue)
			blockchainInstance, err = blockchain.NewBlockchain(storage, &height)
		} else {
			blockchainInstance, err = blockchain.NewBlockchain(storage, nil)
		}
		if err != nil {
			return err
		}
		return fn(blockchainInstance, storage)
	})
	if err != nil {
		return fmt.Errorf("Failed to initialize storage. %v", err)
	}
	return nil
}

func run(cliContext *cli.Context) error {
	utils.Ftracef(cliContext.App.Writer, defaults.UserAgent)

	return withBlockchain(cliContext, func(blockchain *blockchain.Blockchain, s storage.Storage) error {
		height, safeboxHash, cumulativeDifficulty := blockchain.GetState()
		utils.Ftracef(cliContext.App.Writer, "Blockchain loaded, height %d safeboxHash %s cumulativeDifficulty %s", height, hex.EncodeToString(safeboxHash), cumulativeDifficulty.String())

		config := network.Config{
			ListenAddr:     fmt.Sprintf("%s:%d", defaults.P2PBindAddress, cliContext.GlobalUint(p2pPortFlag.GetName())),
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

				if cliContext.IsSet(exclusiveNodesFlag.GetName()) {
					for _, hostPort := range strings.Split(cliContext.String(exclusiveNodesFlag.GetName()), ",") {
						if err = node.AddPeer("tcp", hostPort); err != nil {
							utils.Ftracef(cliContext.App.Writer, "Failed to add bootstrap peer %s: %v", hostPort, err)
						}
					}
				} else {
					s.LoadPeers(func(address []byte, data []byte) {
						if err = node.AddPeerSerialized("tcp", data); err != nil {
							utils.Ftracef(cliContext.App.Writer, "Failed to load peer data: %v", err)
						}
					})
					for _, hostPort := range strings.Split(defaults.BootstrapNodes, ",") {
						if err = node.AddPeer("tcp", hostPort); err != nil {
							utils.Ftracef(cliContext.App.Writer, "Failed to add bootstrap peer %s: %v", hostPort, err)
						}
					}
					defer func() {
						peers := node.GetPeersByNetwork("tcp")
						s.WithWritable(func(s storage.StorageWritable, ctx interface{}) error {
							return s.StorePeers(ctx, func(fn func(address []byte, data []byte)) {
							for address := range peers {
								fn([]byte(address), utils.Serialize(peers[address]))
							}
						})
						})
					}()

					updatesListener := concurrent.NewUnboundedExecutor()
					updatesListener.Go(func(ctx context.Context) {
						for {
							select {
							case peer := <-peerUpdates:
								//utils.Ftracef(cliContext.App.Writer, "   %s:%d last seen %s ago", peer.Host, peer.Port, time.Since(time.Unix(int64(peer.LastConnect), 0)))
								node.AddPeer("tcp", fmt.Sprintf("%s:%d", peer.Host, peer.Port))
							case <-ctx.Done():
								return
							}
						}
					})
					defer updatesListener.StopAndWaitForever()
				}

				return network.WithRpcServer(fmt.Sprintf("%s:%d", defaults.RPCBindAddress, defaults.RPCPort), api.NewApi(blockchain), func() error {
					c := make(chan os.Signal, 2)
					signal.Notify(c, os.Interrupt, syscall.SIGTERM)
					<-c
					utils.Ftracef(cliContext.App.Writer, "Exit signal received. Terminating...")
					return nil
				})
			})
		})
	})
}

func main() {
	app := cli.NewApp()
	app.Usage = "PASL command line interface"
	app.Version = defaults.UserAgent
	app.Action = run
	app.Commands = []cli.Command{
		exportCommand,
		getCommand,
	}
	app.Flags = []cli.Flag{
		dataDirFlag,
		exclusiveNodesFlag,
		heightFlag,
		p2pPortFlag,
	}
	app.CommandNotFound = func(c *cli.Context, command string) {
		cli.ShowAppHelp(c)
		os.Exit(1)
	}
	if err := app.Run(os.Args); err != nil {
		utils.Panicf("Error running application: %v", err)
		os.Exit(2)
	}
	os.Exit(0)
}
