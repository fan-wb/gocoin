package main

import (
	"fmt"
	"os"
	"flag"
	"time"
	"bufio"
//	"bytes"
//	"sync"
	"runtime"
	"strings"
	"strconv"
	"github.com/piotrnar/gocoin/btc"
	_ "github.com/piotrnar/gocoin/btc/memdb"
)


var (
	//host *string = flag.String("c", "blockchain.info:8333", "Connect to specified host")
	//listen *bool = flag.Bool("l", false, "Listen insted of connecting")
	verbose *bool = flag.Bool("v", false, "Verbose mode")
	testnet *bool = flag.Bool("t", false, "Use Testnet3")
	rescan *bool = flag.Bool("rescan", false, "Rescan unspent outputs (not scripts)")

	GenesisBlock *btc.Uint256
	Magic [4]byte
	BlockChain *btc.Chain

	dbg uint64

	pendingBlocks map[[btc.Uint256IdxLen]byte] []byte = make(map[[btc.Uint256IdxLen]byte] []byte)
	askForDataCnt int32
	cachedBlocks map[[btc.Uint256IdxLen]byte] *btc.Block = make(map[[btc.Uint256IdxLen]byte] *btc.Block)
)


func init_blockchain() {
	if *testnet { // testnet3
		GenesisBlock = btc.NewUint256FromString("000000000933ea01ad0ee984209779baaec3ced90fa3f408719526f8d77f4943")
		Magic = [4]byte{0x0B,0x11,0x09,0x07}
	} else {
		GenesisBlock = btc.NewUint256FromString("000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f")
		Magic = [4]byte{0xF9,0xBE,0xB4,0xD9}
	}

	BlockChain = btc.NewChain(GenesisBlock, *rescan)
}


func do_userif() {
	for {
		li, _, _ := bufio.NewReader(os.Stdin).ReadLine()
		if len(li) > 0 {
			c := new(command)
			c.src = "ui"
			c.str = string(li[:])
			cmdChannel <- c
		}
	}
}



func list_unspent(addr string) {
	fmt.Println("Checking unspent coins for addr", addr)
	a, e := btc.NewAddrFromString(addr)
	if e != nil {
		println(e.Error())
		return
	}
	unsp := BlockChain.GetAllUnspent(a)
	var sum uint64
	for i := range unsp {
		fmt.Println(unsp[i].String())
		sum += unsp[i].Value
	}
	fmt.Printf("Total %.8f unspent BTC at address %s\n", float64(sum)/1e8, a.Enc58str);
}


func show_stats() {
	fmt.Printf("Blocks in the cache : %d.  Pending blocks : %d\n", 
		len(cachedBlocks), len(pendingBlocks))
	fmt.Println(BlockChain.Stats())
}

func show_invs() {
	mutex.Lock()
	fmt.Println(len(pendingBlocks), "pending invs")
	for k, _ := range pendingBlocks {
		fmt.Println(btc.NewUint256(k[:]).String())
	}
	mutex.Unlock()
}


func show_cached() {
	for _, v := range cachedBlocks {
		fmt.Printf(" * %s -> %s\n", v.Hash.String(), btc.NewUint256(v.Parent).String())
	}
}

func retry_cached_blocks() {
start_over:
	for k, v := range cachedBlocks {
		e := BlockChain.AcceptBlock(v)
		if e == nil {
			//println("*** Old block accepted", BlockChain.BlockTreeEnd.Height)
			delete(cachedBlocks, k)
			goto start_over
		} else if e.Error()!=btc.ErrParentNotFound {
			panic(e.Error())
		}
	}
}


func block_fetcher() {
	next_ask_for_block := time.Now()
	for {
		time.Sleep(1e9)
		mutex.Lock()
		if len(pendingBlocks)==0 {
			if askForBlocks==nil && time.Now().After(next_ask_for_block) {
				askForBlocks = BlockChain.BlockTreeEnd.BlockHash.Hash[:]
				//println("ask4bl", BlockChain.BlockTreeEnd.Height, BlockChain.BlockTreeEnd.BlockHash.String())
				next_ask_for_block = time.Now().Add(10*time.Second)
			}
		} else {
			if askForDataCnt==0 && askForData==nil {
				for _, v := range pendingBlocks {
					//println("ask4dat", btc.NewUint256(v).String())
					askForData = v
					askForDataCnt++
					break
				}
			}
		}
		mutex.Unlock()
	}
}


func main() {
	if flag.Lookup("h") != nil {
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.Parse()

	init_blockchain()

	var host string
	
	if *testnet {
		host = "127.0.0.1:18333"
	} else {
		host = "127.0.0.1:8333"
	}

	go do_network(host)
	go do_userif()
	go block_fetcher()

	for {
		msg := <- cmdChannel
		//println("got msg", msg.src)
		if msg.src=="ui" {
			if strings.HasPrefix(msg.str, "unspent") {
				list_unspent(strings.Trim(msg.str[7:], " "))
			} else if strings.HasPrefix(msg.str, "dbg") {
				dbg, _ = strconv.ParseUint(msg.str[3:], 10, 64)
			} else {
				sta := time.Now().UnixNano()
				switch msg.str {
					case "i": 
						show_stats()

					case "info": 
						show_stats()
					
					case "cach": 
						show_cached()
					
					case "invs": 
						show_invs()
					
					case "prof": 
						btc.ShowProfileData()
					
					case "save": 
						fmt.Println("Saving coinbase...")
						BlockChain.Save()
					
					case "quit": 
						fmt.Println("Saving coinbase & quitting...")
						BlockChain.Save()
						goto exit
					
					case "q!": 
						goto exit
					
					case "mem":
						var ms runtime.MemStats
						runtime.ReadMemStats(&ms)
						fmt.Println("HeapAlloc", ms.HeapAlloc>>20, "MB")
					
					default:
						println("unknown command")
				}
				sto := time.Now().UnixNano()
				fmt.Printf("Ready in %.3fs\n", float64(sto-sta)/1e9)
			}
		} else if msg.src=="net" {
			switch msg.str {
				case "invbl":
					ha := btc.NewUint256(msg.dat)
					//fmt.Println("invbl", ha.String())
					idx := ha.BIdx()
					mutex.Lock()
					if _, ok := pendingBlocks[idx]; ok {
						println(ha.String(), "already pending")
					} else if _, ok := cachedBlocks[idx]; ok {
						println(ha.String(), "already received")
					} else if _, ok := BlockChain.BlockIndex[idx]; ok {
						println(ha.String(), "already accepted")
					} else {
						pendingBlocks[idx] = msg.dat
						//println(" - accepted")
					}
					mutex.Unlock()
				
				case "bl":
					bl, e := btc.NewBlock(msg.dat[:])
					if e == nil {
						println("bl", bl.Hash.String())
						mutex.Lock()
						delete(pendingBlocks, bl.Hash.BIdx())
						askForDataCnt--
						mutex.Unlock()
						e = bl.CheckBlock()
						if e == nil {
							e = BlockChain.AcceptBlock(bl)
							if e == nil {
								print("\007")
								//println("*** New block accepted", BlockChain.BlockTreeEnd.Height)
								retry_cached_blocks()
							} else if e.Error()==btc.ErrParentNotFound {
								cachedBlocks[bl.Hash.BIdx()] = bl
								//println("Store block", bl.Hash.String(), "->", bl.GetParent().String(), "for later", len(blocksWithNoParent))
							} else {
								println("AcceptBlock:", e.Error())
							}
						} else {
							println("CheckBlock:", e.Error())
						}
					} else {
						println("NewBlock:", e.Error())
					}
			}
		}
	}
exit:
	println("Closing blockchain")
	BlockChain.Close()
}
