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
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modern-go/concurrent"
	"github.com/urfave/cli"

	"github.com/pasl-project/pasl/api"
	"github.com/pasl-project/pasl/blockchain"
	"github.com/pasl-project/pasl/crypto"
	"github.com/pasl-project/pasl/defaults"
	"github.com/pasl-project/pasl/network"
	"github.com/pasl-project/pasl/network/pasl"
	"github.com/pasl-project/pasl/safebox"
	"github.com/pasl-project/pasl/storage"
	"github.com/pasl-project/pasl/utils"
	"github.com/pasl-project/pasl/wallet"
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

func getBlock(ctx *cli.Context) error {
	if !ctx.Args().Present() {
		return errors.New("invalid block index")
	}
	return withBlockchain(ctx, func(_ *blockchain.Blockchain, s storage.Storage) error {
		index, err := strconv.ParseUint(ctx.Args().First(), 10, 32)
		if err != nil {
			return err
		}
		data, err := s.GetBlock(uint32(index))
		if err != nil {
			return err
		}
		fmt.Fprintf(ctx.App.Writer, "%x\n", data)
		return nil
	})
}

func getHeight(ctx *cli.Context) error {
	return withBlockchain(ctx, func(blockchain *blockchain.Blockchain, _ storage.Storage) error {
		height := blockchain.GetHeight()
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
		{
			Action:      getBlock,
			Name:        "block",
			Usage:       "Get raw block data",
			Description: "",
		},
	},
}

var p2pPortFlag = cli.UintFlag{
	Name:  "p2p-bind-port",
	Usage: "P2P bind port",
	Value: uint(defaults.P2PPort),
}
var rpcIPFlag = cli.StringFlag{
	Name:  "rpc-bind-ip",
	Usage: "RPC bind ip",
	Value: defaults.RPCBindHost,
}
var dataDirFlag = cli.StringFlag{
	Name:  "data-dir",
	Usage: "Directory to store blockchain files",
}
var exclusiveNodesFlag = cli.StringFlag{
	Name:  "exclusive-nodes",
	Usage: "Comma-separated ip:port list of exclusive nodes to connect to",
}
var walletFileFlag = cli.StringFlag{
	Name:  "wallet-file",
	Usage: "File to store encrypted wallet keys",
	Value: "",
}
var passwordFlag = cli.StringFlag{
	Name:  "password",
	Usage: "Password to decrypt wallet keys",
	Value: "",
}

func initWallet(ctx *cli.Context, coreRPCAddress string) (*wallet.Wallet, error) {
	dataDir, err := getDataDir(ctx, false)
	if err != nil {
		return nil, err
	}
	filename := ctx.GlobalString(walletFileFlag.GetName())
	if filename == "" {
		filename = filepath.Join(dataDir, "wallet.json")
	}

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open wallet file '%v': %v", filename, err)
	}

	contents, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, err
	}

	set := func(contents []byte) error {
		if err := file.Truncate(0); err != nil {
			return err
		}
		written, err := file.WriteAt(contents, 0)
		if err != nil {
			return err
		}
		if written != len(contents) {
			return fmt.Errorf("incomplete write")
		}
		return nil
	}

	return wallet.NewWallet(contents, []byte(ctx.GlobalString(passwordFlag.GetName())), set, coreRPCAddress)
}

func getDataDir(ctx *cli.Context, create bool) (string, error) {
	dataDir := ctx.GlobalString(dataDirFlag.GetName())
	if dataDir == "" {
		var err error
		if dataDir, err = utils.GetDataDir(); err != nil {
			return "", fmt.Errorf("Failed to obtain valid data directory path. Use %s flag to manually specify data directory location. Error: %v", dataDirFlag.GetName(), err)
		}
	}

	if create {
		if err := utils.CreateDirectory(&dataDir); err != nil {
			return "", fmt.Errorf("Failed to create data directory %v", err)
		}
	}

	return dataDir, nil
}

func withBlockchain(ctx *cli.Context, fn func(blockchain *blockchain.Blockchain, storage storage.Storage) error) error {
	dataDir, err := getDataDir(ctx, true)
	if err != nil {
		return err
	}

	dbFileName := filepath.Join(dataDir, "storage.db")
	err = storage.WithStorage(&dbFileName, func(storage storage.Storage) (err error) {
		var blockchainInstance *blockchain.Blockchain
		if ctx.IsSet(heightFlag.GetName()) {
			var height uint32
			height = uint32(heightFlagValue)
			blockchainInstance, err = blockchain.NewBlockchain(safebox.NewSafebox, storage, &height)
		} else {
			blockchainInstance, err = blockchain.NewBlockchain(safebox.NewSafebox, storage, nil)
		}
		if err != nil {
			return err
		}
		return fn(blockchainInstance, storage)
	})
	if err != nil {
		return err
	}
	return nil
}

type SignalCancel struct{}

func (SignalCancel) String() string {
	return "Cancal"
}

func (SignalCancel) Signal() {
}

func run(cliContext *cli.Context) error {
	utils.Ftracef(cliContext.App.Writer, defaults.UserAgent)

	utils.Ftracef(cliContext.App.Writer, "Loading blockchain")
	return withBlockchain(cliContext, func(blockchain *blockchain.Blockchain, s storage.Storage) error {
		height, safeboxHash, cumulativeDifficulty := blockchain.GetState()
		utils.Ftracef(cliContext.App.Writer, "Blockchain loaded, height %d safeboxHash %s cumulativeDifficulty %s", height, hex.EncodeToString(safeboxHash), cumulativeDifficulty.String())

		p2pPort := uint16(cliContext.GlobalUint(p2pPortFlag.GetName()))
		config := network.Config{
			ListenAddr:     fmt.Sprintf("%s:%d", defaults.P2PBindAddress, p2pPort),
			MaxIncoming:    defaults.MaxIncoming,
			MaxOutgoing:    defaults.MaxOutgoing,
			TimeoutConnect: defaults.TimeoutConnect,
		}

		key, err := crypto.NewKeyByType(crypto.NIDsecp256k1)
		if err != nil {
			return err
		}
		nonce := utils.Serialize(key.Public)

		peers := network.NewPeersList()
		peerUpdates := make(chan network.PeerInfo)
		return pasl.WithManager(nonce, blockchain, p2pPort, peers, peerUpdates, blockchain.BlocksUpdates, blockchain.TxPoolUpdates, defaults.TimeoutRequest, func(manager *pasl.Manager) error {
			return network.WithNode(config, peers, peerUpdates, manager.OnNewConnection, func(node network.Node) error {
				cancel := make(chan os.Signal, 2)
				coreRPC := api.NewApi(blockchain)
				RPCBindAddress := fmt.Sprintf("%s:%d", cliContext.GlobalString(rpcIPFlag.GetName()), defaults.RPCPort)

				wallet, err := initWallet(cliContext, RPCBindAddress)
				if err != nil {
					return fmt.Errorf("failed to initialize wallet: %v", err)
				}
				defer wallet.Close()
				ln, err := net.Listen("tcp", "127.0.0.1:8100")
				if err != nil {
					return fmt.Errorf("failed to bind Web UI port: %v", err)
				}
				defer ln.Close()
				go func() {
					utils.Ftracef(cliContext.App.Writer, fmt.Sprintf("Web UI is available at http://%s", ln.Addr().String()))
					mux := http.NewServeMux()
					mux.Handle("/", http.FileServer(AssetFile()))
					// TODO: handle error
					http.Serve(ln, mux)
				}()

				if cliContext.IsSet(exclusiveNodesFlag.GetName()) {
					for _, hostPort := range strings.Split(cliContext.String(exclusiveNodesFlag.GetName()), ",") {
						if err = node.AddPeer(hostPort); err != nil {
							utils.Ftracef(cliContext.App.Writer, "Failed to add bootstrap peer %s: %v", hostPort, err)
						}
					}
				} else {
					populatePeers := concurrent.NewUnboundedExecutor()
					populatePeers.Go(func(ctx context.Context) {
						s.LoadPeers(func(address []byte, data []byte) {
							if err = node.AddPeerSerialized(data); err != nil {
								utils.Ftracef(cliContext.App.Writer, "Failed to load peer data: %v", err)
							}
						})
						for _, hostPort := range strings.Split(defaults.BootstrapNodes, ",") {
							if err = node.AddPeer(hostPort); err != nil {
								utils.Ftracef(cliContext.App.Writer, "Failed to add bootstrap peer %s: %v", hostPort, err)
							}
						}
					})
					defer populatePeers.StopAndWaitForever()
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
				}

				RPCHandlers := coreRPC.GetHandlers()
				for k, v := range wallet.GetHandlers() {
					RPCHandlers[k] = v
				}
				return network.WithRpcServer(RPCBindAddress, RPCHandlers, func() error {
					signal.Notify(cancel, os.Interrupt, syscall.SIGTERM)
					<-cancel
					utils.Ftracef(cliContext.App.Writer, "Exit signal received. Terminating...")
					return nil
				})
			})
		})
	})
}

func main() {
	rand.Seed(time.Now().UnixNano())
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
		rpcIPFlag,

		walletFileFlag,
		passwordFlag,
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
