package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ledgerwatch/bolt"
	chart "github.com/wcharczuk/go-chart"
	"github.com/wcharczuk/go-chart/util"

	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/consensus/ethash"
	"github.com/ledgerwatch/turbo-geth/consensus/misc"
	"github.com/ledgerwatch/turbo-geth/core"
	"github.com/ledgerwatch/turbo-geth/core/rawdb"
	"github.com/ledgerwatch/turbo-geth/core/state"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/core/vm"
	"github.com/ledgerwatch/turbo-geth/crypto"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/params"
	"github.com/ledgerwatch/turbo-geth/rlp"
	"github.com/ledgerwatch/turbo-geth/trie"
)

var emptyCodeHash = crypto.Keccak256(nil)
var emptyRoot = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421").Bytes()

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile `file`")
var reset = flag.Int("reset", -1, "reset to given block number")
var rewind = flag.Int("rewind", 1, "rewind to given number of blocks")
var block = flag.Int("block", 1, "specifies a block number for operation")
var account = flag.String("account", "0x", "specifies account to investigate")

func bucketList(db *bolt.DB) [][]byte {
	bucketList := [][]byte{}
	err := db.View(func(tx *bolt.Tx) error {
		err := tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			if len(name) == 20 || bytes.Equal(name, []byte("AT")) {
				n := make([]byte, len(name))
				copy(n, name)
				bucketList = append(bucketList, n)
			}
			return nil
		})
		return err
	})
	if err != nil {
		panic(fmt.Sprintf("Could view db: %s", err))
	}
	return bucketList
}

// prefixLen returns the length of the common prefix of a and b.
func prefixLen(a, b []byte) int {
	var i, length = 0, len(a)
	if len(b) < length {
		length = len(b)
	}
	for ; i < length; i++ {
		if a[i] != b[i] {
			break
		}
	}
	return i
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func parseFloat64(str string) float64 {
	v, _ := strconv.ParseFloat(str, 64)
	return v
}

func readData(filename string) (blocks []float64, hours []float64, dbsize []float64, trienodes []float64, heap []float64) {
	err := util.File.ReadByLines(filename, func(line string) error {
		parts := strings.Split(line, ",")
		blocks = append(blocks, parseFloat64(strings.Trim(parts[0], " ")))
		hours = append(hours, parseFloat64(strings.Trim(parts[1], " ")))
		dbsize = append(dbsize, parseFloat64(strings.Trim(parts[2], " ")))
		trienodes = append(trienodes, parseFloat64(strings.Trim(parts[3], " ")))
		heap = append(heap, parseFloat64(strings.Trim(parts[4], " ")))
		return nil
	})
	if err != nil {
		fmt.Println(err.Error())
	}
	return
}

func notables() []chart.GridLine {
	return []chart.GridLine{
		{Value: 1.0},
		{Value: 2.0},
		{Value: 3.0},
		{Value: 4.0},
		{Value: 5.0},
		{Value: 6.0},
	}
}

func days() []chart.GridLine {
	return []chart.GridLine{
		{Value: 24.0},
		{Value: 48.0},
		{Value: 72.0},
		{Value: 96.0},
		{Value: 120.0},
		{Value: 144.0},
		{Value: 168.0},
		{Value: 192.0},
		{Value: 216.0},
		{Value: 240.0},
		{Value: 264.0},
		{Value: 288.0},
	}
}

func mychart() {
	blocks, hours, dbsize, trienodes, heap := readData("geth.csv")
	//blocks0, hours0, _, _, _ := readData("geth.csv")
	mainSeries := &chart.ContinuousSeries{
		Name: "Cumulative sync time (SSD)",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorBlue,
			FillColor:   chart.ColorBlue.WithAlpha(100),
		},
		XValues: blocks,
		YValues: hours,
	}
	/*
		hddSeries := &chart.ContinuousSeries{
			Name: "Cumulative sync time (HDD)",
			Style: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorRed,
				FillColor:   chart.ColorRed.WithAlpha(100),
			},
			XValues: blocks0,
			YValues: hours0,
		}
	*/
	dbsizeSeries := &chart.ContinuousSeries{
		Name: "Database size",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorBlack,
		},
		YAxis:   chart.YAxisSecondary,
		XValues: blocks,
		YValues: dbsize,
	}

	graph1 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		YAxis: chart.YAxis{
			Name:      "Elapsed time",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%d h", int(v.(float64)))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorBlue,
				StrokeWidth: 1.0,
			},
			GridLines: days(),
		},
		YAxisSecondary: chart.YAxis{
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%d G", int(v.(float64)))
			},
		},
		XAxis: chart.XAxis{
			Name: "Blocks, million",
			Style: chart.Style{
				Show: true,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.3fm", v.(float64))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorAlternateGray,
				StrokeWidth: 1.0,
			},
			GridLines: notables(),
		},
		Series: []chart.Series{
			mainSeries,
			//hddSeries,
			dbsizeSeries,
		},
	}

	graph1.Elements = []chart.Renderable{chart.LegendThin(&graph1)}

	buffer := bytes.NewBuffer([]byte{})
	err := graph1.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart1.png", buffer.Bytes(), 0644)
	check(err)

	heapSeries := &chart.ContinuousSeries{
		Name: "Allocated heap",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorYellow,
			FillColor:   chart.ColorYellow.WithAlpha(100),
		},
		XValues: blocks,
		YValues: heap,
	}
	trienodesSeries := &chart.ContinuousSeries{
		Name: "Trie nodes",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorGreen,
		},
		YAxis:   chart.YAxisSecondary,
		XValues: blocks,
		YValues: trienodes,
	}
	graph2 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		YAxis: chart.YAxis{
			Name:      "Allocated heap",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.1f G", v.(float64))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorYellow,
				StrokeWidth: 1.0,
			},
			GridLines: days(),
		},
		YAxisSecondary: chart.YAxis{
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.1f m", v.(float64))
			},
		},
		XAxis: chart.XAxis{
			Name: "Blocks, million",
			Style: chart.Style{
				Show: true,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.3fm", v.(float64))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorAlternateGray,
				StrokeWidth: 1.0,
			},
			GridLines: notables(),
		},
		Series: []chart.Series{
			heapSeries,
			trienodesSeries,
		},
	}

	graph2.Elements = []chart.Renderable{chart.LegendThin(&graph2)}
	buffer.Reset()
	err = graph2.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart2.png", buffer.Bytes(), 0644)
	check(err)
}

func accountSavings(db *bolt.DB) (int, int) {
	emptyRoots := 0
	emptyCodes := 0
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("AT"))
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if bytes.Index(v, emptyRoot) != -1 {
				emptyRoots++
			}
			if bytes.Index(v, emptyCodeHash) != -1 {
				emptyCodes++
			}
		}
		return nil
	})
	return emptyRoots, emptyCodes
}

func allBuckets(db *bolt.DB) [][]byte {
	bucketList := [][]byte{}
	err := db.View(func(tx *bolt.Tx) error {
		err := tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			n := make([]byte, len(name))
			copy(n, name)
			bucketList = append(bucketList, n)
			return nil
		})
		return err
	})
	if err != nil {
		panic(fmt.Sprintf("Could view db: %s", err))
	}
	return bucketList
}

func printBuckets(db *bolt.DB) {
	bucketList := allBuckets(db)
	for _, bucket := range bucketList {
		fmt.Printf("%s\n", bucket)
	}
}

func bucketStats(db *bolt.DB) {
	bucketList := allBuckets(db)
	storageStats := new(bolt.BucketStats)
	hStorageStats := new(bolt.BucketStats)
	fmt.Printf(",BranchPageN,BranchOverflowN,LeafPageN,LeafOverflowN,KeyN,Depth,BranchAlloc,BranchInuse,LeafAlloc,LeafInuse,BucketN,InlineBucketN,InlineBucketInuse\n")
	db.View(func(tx *bolt.Tx) error {
		for _, bucket := range bucketList {
			b := tx.Bucket(bucket)
			bs := b.Stats()
			if len(bucket) == 20 {
				storageStats.Add(bs)
			} else if len(bucket) == 21 {
				hStorageStats.Add(bs)
			} else {
				fmt.Printf("%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n", string(bucket),
					bs.BranchPageN, bs.BranchOverflowN, bs.LeafPageN, bs.LeafOverflowN, bs.KeyN, bs.Depth, bs.BranchAlloc, bs.BranchInuse,
					bs.LeafAlloc, bs.LeafInuse, bs.BucketN, bs.InlineBucketN, bs.InlineBucketInuse)
			}
		}
		return nil
	})
	bs := *storageStats
	fmt.Printf("%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n", "Contract Storage",
		bs.BranchPageN, bs.BranchOverflowN, bs.LeafPageN, bs.LeafOverflowN, bs.KeyN, bs.Depth, bs.BranchAlloc, bs.BranchInuse,
		bs.LeafAlloc, bs.LeafInuse, bs.BucketN, bs.InlineBucketN, bs.InlineBucketInuse)
	bs = *hStorageStats
	fmt.Printf("%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n", "Contract hStorage",
		bs.BranchPageN, bs.BranchOverflowN, bs.LeafPageN, bs.LeafOverflowN, bs.KeyN, bs.Depth, bs.BranchAlloc, bs.BranchInuse,
		bs.LeafAlloc, bs.LeafInuse, bs.BucketN, bs.InlineBucketN, bs.InlineBucketInuse)
}

func printOccupancies(t *trie.Trie, db ethdb.Database, blockNr uint64) {
	o := make(map[int]map[int]int)
	t.CountOccupancies(db, blockNr, o)
	for level, lo := range o {
		for i, count := range lo {
			fmt.Printf("[%d %d]:%d ", level, i, count)
		}
	}
	fmt.Printf("\n")
}

func trieStats() {
	//db, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	db, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	lastHash := rawdb.ReadHeadHeaderHash(db)
	lastNumber := rawdb.ReadHeaderNumber(db, lastHash)
	lastHeader := rawdb.ReadHeader(db, lastHash, *lastNumber)
	tds, err := state.NewTrieDbState(lastHeader.Root, db, *lastNumber)
	if err != nil {
		panic(err)
	}
	t := tds.AccountTrie()
	printOccupancies(t, db, *lastNumber)
	/*
		statedb := state.New(triedbst)
		t := statedb.GetTrie()
		for i := 0; i < 1; i++ {
			//h1 := t.ResolveRoot(db, lastHeader.Root, lastNumber, i)
			//fmt.Printf("Resolved hash for %d: %s\n", i, h1)
			startTime := time.Now()
			h2 := t.Rebuild(db, lastNumber, i)
			fmt.Printf("Rebuding took %s\n", time.Since(startTime))
			fmt.Printf("Rebult to %s, actual root %x\n", h2, lastHeader.Root)
			fmt.Printf("\n\n")
			//if !bytes.Equal(h1, h2) || !matched {
			//	fmt.Printf("DIFFERENT ROOTS: %d %s %s\n", i, h1, h2)
			//	break
			//}
		}
		statedb.PrintOccupancies()

		fmt.Printf("%x %x\n", lastHeader.Root, statedb.IntermediateRoot(true))
		triedb, tree, err := statedb.EnumerateAccounts()
		if err != nil {
			panic(err)
		}
		sectrie, _ := triedb.(*trie.SecureTrie)
		t := sectrie.GetTrie()
		printOccupancies(t, db, lastNumber)
		nextThreshold := big.NewInt(0)
		step := big.NewInt(1)
		tree.AscendGreaterOrEqual(&state.AccountItem{SecKey: nil, Balance: big.NewInt(0)}, func(i llrb.Item) bool {
			item := i.(*state.AccountItem)
			if item.Balance.Cmp(nextThreshold) != -1 {
				fmt.Printf("Threshold: %s | ", nextThreshold.String())
				printOccupancies(t, db, lastNumber)
				for ; item.Balance.Cmp(nextThreshold) != -1; {
					nextThreshold = nextThreshold.Add(nextThreshold, step)
				}
			}
			t.TryDelete(db, item.SecKey, lastNumber)
			return true
		})
		fmt.Printf("Final check | ")
		printOccupancies(t, db, lastNumber)
	*/
}

func readTrieLog() ([]float64, map[int][]float64, []float64) {
	data, err := ioutil.ReadFile("dust/hack.log")
	check(err)
	thresholds := []float64{}
	counts := map[int][]float64{}
	for i := 2; i <= 16; i++ {
		counts[i] = []float64{}
	}
	shorts := []float64{}
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("Threshold:")) {
			tokens := bytes.Split(line, []byte(" "))
			if len(tokens) == 23 {
				wei := parseFloat64(string(tokens[1]))
				thresholds = append(thresholds, wei)
				for i := 2; i <= 16; i++ {
					pair := bytes.Split(tokens[i+3], []byte(":"))
					counts[i] = append(counts[i], parseFloat64(string(pair[1])))
				}
				pair := bytes.Split(tokens[21], []byte(":"))
				shorts = append(shorts, parseFloat64(string(pair[1])))
			}
		}
	}
	return thresholds, counts, shorts
}

func ts() []chart.GridLine {
	return []chart.GridLine{
		{Value: 420.0},
	}
}

func trieChart() {
	thresholds, counts, shorts := readTrieLog()
	fmt.Printf("%d %d %d\n", len(thresholds), len(counts), len(shorts))
	shortsSeries := &chart.ContinuousSeries{
		Name: "Short nodes",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorBlue,
			FillColor:   chart.ColorBlue.WithAlpha(100),
		},
		XValues: thresholds,
		YValues: shorts,
	}
	countSeries := make(map[int]*chart.ContinuousSeries)
	for i := 2; i <= 16; i++ {
		countSeries[i] = &chart.ContinuousSeries{
			Name: fmt.Sprintf("%d-nodes", i),
			Style: chart.Style{
				Show:        true,
				StrokeColor: chart.GetAlternateColor(i),
			},
			XValues: thresholds,
			YValues: counts[i],
		}
	}
	xaxis := &chart.XAxis{
		Name: "Dust theshold",
		Style: chart.Style{
			Show: true,
		},
		ValueFormatter: func(v interface{}) string {
			return fmt.Sprintf("%d wei", int(v.(float64)))
		},
		GridMajorStyle: chart.Style{
			Show:        true,
			StrokeColor: chart.DefaultStrokeColor,
			StrokeWidth: 1.0,
		},
		Range: &chart.LogRange{
			Min: thresholds[0],
			Max: thresholds[len(thresholds)-1],
		},
		Ticks: []chart.Tick{
			{Value: 0.0, Label: "0"},
			{Value: 1.0, Label: "wei"},
			{Value: 10.0, Label: "10"},
			{Value: 100.0, Label: "100"},
			{Value: 1e3, Label: "1e3"},
			{Value: 1e4, Label: "1e4"},
			{Value: 1e5, Label: "1e5"},
			{Value: 1e6, Label: "1e6"},
			{Value: 1e7, Label: "1e7"},
			{Value: 1e8, Label: "1e8"},
			{Value: 1e9, Label: "1e9"},
			{Value: 1e10, Label: "1e10"},
			//{1e15, "finney"},
			//{1e18, "ether"},
		},
	}

	graph3 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		XAxis: *xaxis,
		YAxis: chart.YAxis{
			Name:      "Node count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%dm", int(v.(float64)/1e6))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.DefaultStrokeColor,
				StrokeWidth: 1.0,
			},
		},
		Series: []chart.Series{
			shortsSeries,
		},
	}
	graph3.Elements = []chart.Renderable{chart.LegendThin(&graph3)}
	buffer := bytes.NewBuffer([]byte{})
	err := graph3.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart3.png", buffer.Bytes(), 0644)
	check(err)
	graph4 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		XAxis: *xaxis,
		YAxis: chart.YAxis{
			Name:      "Node count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.2fm", v.(float64)/1e6)
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.DefaultStrokeColor,
				StrokeWidth: 1.0,
			},
		},
		Series: []chart.Series{
			countSeries[2],
			countSeries[3],
		},
	}
	graph4.Elements = []chart.Renderable{chart.LegendThin(&graph4)}
	buffer = bytes.NewBuffer([]byte{})
	err = graph4.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart4.png", buffer.Bytes(), 0644)
	check(err)
	graph5 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		XAxis: *xaxis,
		YAxis: chart.YAxis{
			Name:      "Node count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.2fk", v.(float64)/1e3)
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.DefaultStrokeColor,
				StrokeWidth: 1.0,
			},
		},
		Series: []chart.Series{
			countSeries[4],
			countSeries[5],
			countSeries[6],
			countSeries[7],
			countSeries[8],
			countSeries[9],
			countSeries[10],
			countSeries[11],
			countSeries[12],
			countSeries[13],
			countSeries[14],
			countSeries[15],
			countSeries[16],
		},
	}
	graph5.Elements = []chart.Renderable{chart.LegendThin(&graph5)}
	buffer = bytes.NewBuffer([]byte{})
	err = graph5.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart5.png", buffer.Bytes(), 0644)
	check(err)
}

func execToBlock(block int) {
	blockDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/testnet/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	check(err)
	bcb, err := core.NewBlockChain(blockDb, nil, params.TestnetChainConfig, ethash.NewFaker(), vm.Config{}, nil)
	check(err)
	defer blockDb.Close()
	os.Remove("statedb")
	os.Remove("statedb.hash")
	stateDb, err := ethdb.NewBoltDatabase("statedb")
	check(err)
	defer stateDb.Close()
	_, _, _, err = core.SetupGenesisBlock(stateDb, core.DefaultTestnetGenesisBlock())
	check(err)
	bc, err := core.NewBlockChain(stateDb, nil, params.TestnetChainConfig, ethash.NewFaker(), vm.Config{}, nil)
	check(err)
	bc.SetNoHistory(true)
	blocks := types.Blocks{}
	var lastBlock *types.Block
	for i := 1; i <= block; i++ {
		lastBlock = bcb.GetBlockByNumber(uint64(i))
		blocks = append(blocks, lastBlock)
		if len(blocks) >= 100 || i == block {
			_, err = bc.InsertChain(blocks)
			check(err)
			fmt.Printf("Inserted %d blocks\n", i)
			blocks = types.Blocks{}
		}
	}
	tds := bc.GetTrieDbState()
	root := tds.LastRoot()
	fmt.Printf("Root hash: %x\n", root)
	fmt.Printf("Last block root hash: %x\n", lastBlock.Root())
	filename := fmt.Sprintf("right_%d.txt", lastBlock.NumberU64())
	fmt.Printf("Generating deep snapshot of the right tries... %s\n", filename)
	f, err := os.Create(filename)
	if err == nil {
		defer f.Close()
		tds.PrintTrie(f)
	}
}

func extractTrie(block int) {
	stateDb, err := ethdb.NewBoltDatabase("statedb")
	check(err)
	defer stateDb.Close()
	bc, err := core.NewBlockChain(stateDb, nil, params.TestnetChainConfig, ethash.NewFaker(), vm.Config{}, nil)
	check(err)
	baseBlock := bc.GetBlockByNumber(uint64(block))
	tds, err := state.NewTrieDbState(baseBlock.Root(), stateDb, baseBlock.NumberU64())
	check(err)
	startTime := time.Now()
	tds.Rebuild()
	fmt.Printf("Rebuld done in %v\n", time.Since(startTime))
	rebuiltRoot := tds.LastRoot()
	fmt.Printf("Rebuit root hash: %x\n", rebuiltRoot)
	filename := fmt.Sprintf("right_%d.txt", baseBlock.NumberU64())
	fmt.Printf("Generating deep snapshot of the right tries... %s\n", filename)
	f, err := os.Create(filename)
	if err == nil {
		defer f.Close()
		tds.PrintTrie(f)
	}
}

func testRewind(block, rewind int) {
	ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/testnet/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("statedb")
	check(err)
	defer ethDb.Close()
	bc, err := core.NewBlockChain(ethDb, nil, params.TestnetChainConfig, ethash.NewFaker(), vm.Config{}, nil)
	check(err)
	currentBlock := bc.CurrentBlock()
	currentBlockNr := currentBlock.NumberU64()
	baseBlock := bc.GetBlockByNumber(uint64(block))
	baseBlockNr := baseBlock.NumberU64()
	fmt.Printf("Base block number: %d\n", baseBlockNr)
	fmt.Printf("Base block root hash: %x\n", baseBlock.Root())
	db := ethDb.NewBatch()
	defer db.Rollback()
	tds, err := state.NewTrieDbState(baseBlock.Root(), db, baseBlockNr)
	tds.SetHistorical(baseBlockNr != currentBlockNr)
	check(err)
	startTime := time.Now()
	tds.Rebuild()
	fmt.Printf("Rebuld done in %v\n", time.Since(startTime))
	rebuiltRoot := tds.LastRoot()
	fmt.Printf("Rebuit root hash: %x\n", rebuiltRoot)
	startTime = time.Now()
	rewindLen := uint64(rewind)
	err = tds.UnwindTo(baseBlockNr - rewindLen)
	fmt.Printf("Unwind done in %v\n", time.Since(startTime))
	check(err)
	rewoundBlock_1 := bc.GetBlockByNumber(baseBlockNr - rewindLen + 1)
	fmt.Printf("Rewound+1 block number: %d\n", rewoundBlock_1.NumberU64())
	fmt.Printf("Rewound+1 block hash: %x\n", rewoundBlock_1.Hash())
	fmt.Printf("Rewound+1 block root hash: %x\n", rewoundBlock_1.Root())
	fmt.Printf("Rewound+1 block parent hash: %x\n", rewoundBlock_1.ParentHash())

	rewoundBlock := bc.GetBlockByNumber(baseBlockNr - rewindLen)
	fmt.Printf("Rewound block number: %d\n", rewoundBlock.NumberU64())
	fmt.Printf("Rewound block hash: %x\n", rewoundBlock.Hash())
	fmt.Printf("Rewound block root hash: %x\n", rewoundBlock.Root())
	fmt.Printf("Rewound block parent hash: %x\n", rewoundBlock.ParentHash())
	rewoundRoot := tds.LastRoot()
	fmt.Printf("Calculated rewound root hash: %x\n", rewoundRoot)
	/*
		filename := fmt.Sprintf("root_%d.txt", rewoundBlock.NumberU64())
		fmt.Printf("Generating deep snapshot of the wront tries... %s\n", filename)
		f, err := os.Create(filename)
		if err == nil {
			defer f.Close()
			tds.PrintTrie(f)
		}

		{
			tds, err = state.NewTrieDbState(rewoundBlock.Root(), db, rewoundBlock.NumberU64())
			tds.SetHistorical(true)
			check(err)
			startTime := time.Now()
			tds.Rebuild()
			fmt.Printf("Rebuld done in %v\n", time.Since(startTime))
			rebuiltRoot, err := tds.TrieRoot()
			fmt.Printf("Rebuilt root: %x\n", rebuiltRoot)
			check(err)
		}
	*/
}

func testStartup() {
	startTime := time.Now()
	//ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	check(err)
	defer ethDb.Close()
	bc, err := core.NewBlockChain(ethDb, nil, params.MainnetChainConfig, ethash.NewFaker(), vm.Config{}, nil)
	check(err)
	currentBlock := bc.CurrentBlock()
	currentBlockNr := currentBlock.NumberU64()
	fmt.Printf("Current block number: %d\n", currentBlockNr)
	fmt.Printf("Current block root hash: %x\n", currentBlock.Root())
	t := trie.New(common.Hash{}, state.AccountsBucket, nil, false)
	r := trie.NewResolver(false, true, currentBlockNr)
	key := []byte{}
	rootHash := currentBlock.Root()
	tc := t.NewContinuation(key, 0, rootHash[:])
	r.AddContinuation(tc)
	err = r.ResolveWithDb(ethDb, currentBlockNr)
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	fmt.Printf("Took %v\n", time.Since(startTime))
}

func testResolve() {
	startTime := time.Now()
	//ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	ethDb, err := ethdb.NewBoltDatabase("statedb")
	check(err)
	defer ethDb.Close()
	//treePrefix := common.FromHex("1194e966965418c7d73a42cceeb254d875860356")
	t := trie.New(common.Hash{}, state.AccountsBucket, nil, true)
	r := trie.NewResolver(false, true, 1828653)
	key := common.FromHex("0803040c01")
	resolveHash := common.FromHex("f123ef56888702971ba0604b51d0e229979f8e0b9f719cd18699e1238ab7bb4c")
	tc := t.NewContinuation(key, 5, resolveHash)
	r.AddContinuation(tc)
	err = r.ResolveWithDb(ethDb, 1828653)
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	fmt.Printf("Took %v\n", time.Since(startTime))
	fmt.Printf("%s\n", tc)
}

func hashFile() {
	f, err := os.Open("/Users/alexeyakhunov/mygit/go-ethereum/geth.log")
	check(err)
	defer f.Close()
	w, err := os.Create("/Users/alexeyakhunov/mygit/go-ethereum/geth_read.log")
	check(err)
	defer w.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ResolveWithDb") || strings.HasPrefix(line, "Error") ||
			strings.HasPrefix(line, "0000000000000000000000000000000000000000000000000000000000000000") ||
			strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "tc{") {
			fmt.Printf("%d %s\n", count, line)
			count++
		} else if count == 66 {
			w.WriteString(line)
			w.WriteString("\n")
		}
	}
	fmt.Printf("%d lines scanned\n", count)
}

func rlpIndices() {
	keybuf := new(bytes.Buffer)
	for i := 0; i < 512; i++ {
		keybuf.Reset()
		rlp.Encode(keybuf, uint(i))
		fmt.Printf("Encoding of %d is %x\n", i, keybuf.Bytes())
	}
}

func printFullNodeRLPs() {
	trie.FullNode1()
	trie.FullNode2()
	trie.FullNode3()
	trie.FullNode4()
	trie.ShortNode1()
	trie.ShortNode2()
	trie.Hash1()
	trie.Hash2()
	trie.Hash3()
	trie.Hash4()
	trie.Hash5()
	trie.Hash6()
	trie.Hash7()
}

func testDifficulty() {
	genesisBlock, _, _, err := core.DefaultGenesisBlock().ToBlock(nil)
	check(err)
	d1 := ethash.CalcDifficulty(params.MainnetChainConfig, 100000, genesisBlock.Header())
	fmt.Printf("Block 1 difficulty: %d\n", d1)
}

func testBlockHashes() {
	ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	check(err)
	hash := rawdb.ReadCanonicalHash(ethDb, 823144)
	fmt.Printf("Canonical hash: %x\n", hash)
	header := rawdb.ReadHeader(ethDb, hash, 823144)
	fmt.Printf("Header.TxHash: %x\n", header.TxHash)
	fmt.Printf("Header.UncleHash: %x\n", header.UncleHash)
}

func printTxHashes() {
	ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	check(err)
	defer ethDb.Close()
	for b := uint64(0); b < uint64(100000); b++ {
		hash := rawdb.ReadCanonicalHash(ethDb, b)
		block := rawdb.ReadBlock(ethDb, hash, b)
		if block == nil {
			break
		}
		for _, tx := range block.Transactions() {
			fmt.Printf("%x\n", tx.Hash())
		}
	}
}

func relayoutKeys() {
	//db, err := bolt.Open("/home/akhounov/.ethereum/geth/chaindata", 0600, &bolt.Options{ReadOnly: true})
	db, err := bolt.Open("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata", 0600, &bolt.Options{ReadOnly: true})
	check(err)
	defer db.Close()
	var count int
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("SUFFIX"))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			count++
		}
		return nil
	})
	check(err)
	fmt.Printf("Records: %d\n", count)
}

func upgradeBlocks() {
	//ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	check(err)
	defer ethDb.Close()
	start := []byte{}
	var keys [][]byte
	if err := ethDb.Walk([]byte("b"), start, 0, func(k, v []byte) (bool, error) {
		if len(keys)%1000 == 0 {
			fmt.Printf("Collected keys: %d\n", len(keys))
		}
		keys = append(keys, common.CopyBytes(k))
		return true, nil
	}); err != nil {
		panic(err)
	}
	for i, key := range keys {
		v, err := ethDb.Get([]byte("b"), key)
		if err != nil {
			panic(err)
		}
		smallBody := new(types.SmallBody) // To be changed to SmallBody
		if err := rlp.Decode(bytes.NewReader(v), smallBody); err != nil {
			panic(err)
		}
		body := new(types.Body)
		blockNum := binary.BigEndian.Uint64(key[:8])
		signer := types.MakeSigner(params.MainnetChainConfig, big.NewInt(int64(blockNum)))
		body.Senders = make([]common.Address, len(smallBody.Transactions))
		for j, tx := range smallBody.Transactions {
			addr, err := signer.Sender(tx)
			if err != nil {
				panic(err)
			}
			body.Senders[j] = addr
		}
		body.Transactions = smallBody.Transactions
		body.Uncles = smallBody.Uncles
		newV, err := rlp.EncodeToBytes(body)
		if err != nil {
			panic(err)
		}
		ethDb.Put([]byte("b"), key, newV)
		if i%1000 == 0 {
			fmt.Printf("Upgraded keys: %d\n", i)
		}
	}
	check(ethDb.DeleteBucket([]byte("r")))
}

func readTrie(filename string, encodeToBytes bool) *trie.Trie {
	f, err := os.Open(filename)
	check(err)
	defer f.Close()
	t, err := trie.Load(f, encodeToBytes)
	check(err)
	return t
}

func invTree(wrong, right, diff string, block int, encodeToBytes bool) {
	fmt.Printf("Reading trie...\n")
	t1 := readTrie(fmt.Sprintf("%s_%d.txt", wrong, block), encodeToBytes)
	fmt.Printf("Root hash: %x\n", t1.Hash())
	fmt.Printf("Reading trie 2...\n")
	t2 := readTrie(fmt.Sprintf("%s_%d.txt", right, block), encodeToBytes)
	fmt.Printf("Root hash: %x\n", t2.Hash())
	c, err := os.Create(fmt.Sprintf("%s_%d.txt", diff, block))
	check(err)
	defer c.Close()
	t1.PrintDiff(t2, c)
}

func preimage() {
	ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/Volumes/tb4/turbo-geth-10/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	check(err)
	defer ethDb.Close()
	p, err := ethDb.Get(trie.SecureKeyPrefix, common.FromHex("0x1c65b0ce81d408e9d99fa990df5bdf129bfc8ddb4458b5ae111a3241e6425a5e"))
	check(err)
	fmt.Printf("%x\n", p)
}

func encodeTimestamp(timestamp uint64) []byte {
	var suffix []byte
	var limit uint64
	limit = 32
	for bytecount := 1; bytecount <= 8; bytecount++ {
		if timestamp < limit {
			suffix = make([]byte, bytecount)
			b := timestamp
			for i := bytecount - 1; i > 0; i-- {
				suffix[i] = byte(b & 0xff)
				b >>= 8
			}
			suffix[0] = byte(b) | (byte(bytecount) << 5) // 3 most significant bits of the first byte are bytecount
			break
		}
		limit <<= 8
	}
	return suffix
}

func decodeTimestamp(suffix []byte) (uint64, []byte) {
	bytecount := int(suffix[0] >> 5)
	timestamp := uint64(suffix[0] & 0x1f)
	for i := 1; i < bytecount; i++ {
		timestamp = (timestamp << 8) | uint64(suffix[i])
	}
	return timestamp, suffix[bytecount:]
}

func loadAccount() {
	ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/Volumes/tb4/turbo-geth/geth/chaindata")
	check(err)
	defer ethDb.Close()
	blockNr := uint64(*block)
	blockSuffix := encodeTimestamp(blockNr)
	accountBytes := common.FromHex(*account)
	secKey := crypto.Keccak256(accountBytes)
	accountData, err := ethDb.GetAsOf(state.AccountsBucket, state.AccountsHistoryBucket, secKey, blockNr+1)
	check(err)
	fmt.Printf("Account data: %x\n", accountData)
	startkey := make([]byte, len(accountBytes)+32)
	copy(startkey, accountBytes)
	t := trie.New(common.Hash{}, state.StorageBucket, accountBytes[:], true)
	count := 0
	if err := ethDb.WalkAsOf(state.StorageBucket, state.StorageHistoryBucket, startkey, uint(len(accountBytes)*8), blockNr, func(k, v []byte) (bool, error) {
		key := k[len(accountBytes):]
		//fmt.Printf("%x: %x\n", key, v)
		t.Update(key, v, blockNr)
		count++
		return true, nil
	}); err != nil {
		panic(err)
	}
	fmt.Printf("After %d updates, reconstructed storage root: %x\n", count, t.Hash())
	var keys [][]byte
	if err := ethDb.Walk(state.StorageHistoryBucket, accountBytes, uint(len(accountBytes)*8), func(k, v []byte) (bool, error) {
		if !bytes.HasSuffix(k, blockSuffix) {
			return true, nil
		}
		key := k[:len(k)-len(blockSuffix)]
		keys = append(keys, common.CopyBytes(key))
		return true, nil
	}); err != nil {
		panic(err)
	}
	fmt.Printf("%d keys updated\n", len(keys))
	for _, k := range keys {
		v, err := ethDb.GetAsOf(state.StorageBucket, state.StorageHistoryBucket, []byte(k), blockNr+1)
		if err != nil {
			fmt.Printf("for key %x err %v\n", k, err)
		}
		v_orig, err := ethDb.GetAsOf(state.StorageBucket, state.StorageHistoryBucket, []byte(k), blockNr)
		if err != nil {
			fmt.Printf("for key %x err %v\n", k, err)
		}
		key := ([]byte(k))[len(accountBytes):]
		if len(v) > 0 {
			fmt.Printf("Updated %x: %x from %x\n", key, v, v_orig)
			t.Update(key, v, blockNr)
			check(err)
		} else {
			fmt.Printf("Deleted %x from %x\n", key, v_orig)
			t.Delete(key, blockNr)
		}
	}
	fmt.Printf("Updated storage root: %x\n", t.Hash())
}

func printBranches(block uint64) {
	ethDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/testnet/geth/chaindata")
	//ethDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	check(err)
	defer ethDb.Close()
	fmt.Printf("All headers at the same height %d\n", block)
	{
		var hashes []common.Hash
		numberEnc := make([]byte, 8)
		binary.BigEndian.PutUint64(numberEnc, block)
		if err := ethDb.Walk([]byte("h"), numberEnc, 8*8, func(k, v []byte) (bool, error) {
			if len(k) == 8+32 {
				hashes = append(hashes, common.BytesToHash(k[8:]))
			}
			return true, nil
		}); err != nil {
			panic(err)
		}
		for _, hash := range hashes {
			h := rawdb.ReadHeader(ethDb, hash, block)
			fmt.Printf("block hash: %x, root hash: %x\n", h.Hash(), h.Root)
		}
	}
}

// Some weird constants to avoid constant memory allocs for them.
var (
	big8  = big.NewInt(8)
	big32 = big.NewInt(32)
)

// accumulateRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func accumulateRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) {
	// select the correct block reward based on chain progression
	blockReward := ethash.FrontierBlockReward
	if config.IsByzantium(header.Number) {
		blockReward = ethash.ByzantiumBlockReward
	}

	// accumulate the rewards for the miner and any included uncles
	reward := new(big.Int).Set(blockReward)
	r := new(big.Int)

	for _, uncle := range uncles {
		r.Add(uncle.Number, big8)
		r.Sub(r, header.Number)
		r.Mul(r, blockReward)
		r.Div(r, big8)
		state.AddBalance(uncle.Coinbase, r)
		r.Div(blockReward, big32)
		reward.Add(reward, r)
	}

	state.AddBalance(header.Coinbase, reward)
}

func repair() {
	sigs := make(chan os.Signal, 1)
	interruptCh := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		interruptCh <- true
	}()

	//historyDb, err := ethdb.NewBoltDatabase("/home/akhounov/.ethereum/geth/chaindata")
	historyDb, err := ethdb.NewBoltDatabase("/Volumes/tb4/turbo-geth-10/geth/chaindata")
	//historyDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata")
	//historyDb, err := ethdb.NewBoltDatabase("/Users/alexeyakhunov/Library/Ethereum/testnet/geth/chaindata")
	//historyDb, err := ethdb.NewBoltDatabase("/Volumes/tb4/turbo-geth/ropsten/geth/chaindata")
	check(err)
	defer historyDb.Close()
	//if *block == 1 {
	//	os.Remove("statedb")
	//	os.Remove("statedb.hash")
	//}
	currentDb := ethdb.NewMemDatabase()
	//check(err)
	defer currentDb.Close()
	if *block == 1 {
		_, _, _, err = core.SetupGenesisBlock(currentDb, core.DefaultGenesisBlock())
		check(err)
	}
	chainConfig := params.MainnetChainConfig
	vmConfig := vm.Config{}
	bc, err := core.NewBlockChain(historyDb, nil, chainConfig, ethash.NewFaker(), vmConfig, nil)
	check(err)
	blockNum := uint64(*block)
	interrupt := false
	noopWriter := state.NewNoopWriter()
	currentM := currentDb.NewBatch()
	dbstate := state.NewRepairDbState(currentM, historyDb, blockNum-1)
	for !interrupt {
		block := bc.GetBlockByNumber(blockNum)
		if block == nil {
			break
		}
		statedb := state.New(dbstate)
		var (
			usedGas = new(uint64)
			gp      = new(core.GasPool).AddGas(block.GasLimit())
		)
		if chainConfig.DAOForkSupport && chainConfig.DAOForkBlock != nil && chainConfig.DAOForkBlock.Cmp(block.Number()) == 0 {
			misc.ApplyDAOHardFork(statedb)
		}
		header := block.Header()
		for _, tx := range block.Transactions() {
			if _, _, err := core.ApplyTransaction(chainConfig, bc, nil, gp, statedb, noopWriter, header, tx, usedGas, vmConfig); err != nil {
				panic(fmt.Errorf("at block %d, tx %x: %v", block.NumberU64(), tx.Hash(), err))
			}
		}
		// apply mining rewards to the geth stateDB
		accumulateRewards(chainConfig, statedb, header, block.Uncles())
		dbstate.SetBlockNr(block.NumberU64())
		if err := statedb.Commit(chainConfig.IsEIP158(block.Number()), dbstate); err != nil {
			panic(err)
		}
		dbstate.CheckKeys()
		if currentM.BatchSize() >= 200000 {
			_, err := currentM.Commit()
			check(err)
			dbstate.PruneTries()
		}
		blockNum++
		if blockNum%100000 == 0 {
			fmt.Printf("Processed %d blocks\n", blockNum)
		}
		// Check for interrupts
		select {
		case interrupt = <-interruptCh:
			fmt.Println("interrupted, please wait for cleanup...")
		default:
		}
	}
	_, err = currentM.Commit()
	check(err)
	fmt.Printf("Next time specify -block %d\n", blockNum)
}

func readAccount() {
	ethDb, err := ethdb.NewBoltDatabase("statedb")
	check(err)
	accountBytes := common.FromHex(*account)
	secKey := crypto.Keccak256(accountBytes)
	v, _ := ethDb.Get(state.AccountsBucket, secKey)
	fmt.Printf("%x:%x\n", secKey, v)
}

func repairCurrent() {
	historyDb, err := bolt.Open("/Volumes/tb4/turbo-geth/ropsten/geth/chaindata", 0600, &bolt.Options{})
	check(err)
	defer historyDb.Close()
	currentDb, err := bolt.Open("statedb", 0600, &bolt.Options{})
	check(err)
	defer currentDb.Close()
	check(historyDb.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(state.StorageBucket); err != nil {
			return err
		}
		newB, err := tx.CreateBucket(state.StorageBucket, true)
		if err != nil {
			return err
		}
		count := 0
		if err := currentDb.View(func(ctx *bolt.Tx) error {
			b := ctx.Bucket(state.StorageBucket)
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				newB.Put(k, v)
				count++
				if count == 10000 {
					fmt.Printf("Copied %d storage items\n", count)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		return nil
	}))
}

func testMemBolt() {
	db, err := bolt.Open("membolt", 0600, &bolt.Options{MemOnly: true})
	check(err)
	defer db.Close()
	err = db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("B"), false)
		if err != nil {
			return fmt.Errorf("Bucket creation: %v", err)
		}
		for i := 0; i < 1000; i++ {
			err = bucket.Put(append([]byte("gjdfigjkdfljgdlfkjg"), []byte(fmt.Sprintf("%d", i))...), []byte("kljklgjfdkljkdjd"))
			if err != nil {
				return fmt.Errorf("Put: %v", err)
			}
		}
		return nil
	})
	check(err)
}

func main() {
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}
	//db, err := bolt.Open("/home/akhounov/.ethereum/geth/chaindata", 0600, &bolt.Options{ReadOnly: true})
	//db, err := bolt.Open("/Users/alexeyakhunov/Library/Ethereum/geth/chaindata", 0600, &bolt.Options{ReadOnly: true})
	//check(err)
	//defer db.Close()
	//bucketStats(db)
	//mychart()
	//testRebuild()
	//testRewind(*block, *rewind)
	//hashFile()
	//buildHashFromFile()
	//testResolve()
	//rlpIndices()
	//printFullNodeRLPs()
	//testStartup()
	//testDifficulty()
	//testRewindTests()
	//if *reset != -1 {
	//	testReset(uint64(*reset))
	//}
	//testBlockHashes()
	//printBuckets(db)
	//printTxHashes()
	//relayoutKeys()
	//testRedis()
	//upgradeBlocks()
	//compareTries()
	invTree("root", "right", "diff", *block, false)
	//invTree("iw", "ir", "id", *block, true)
	//loadAccount()
	//preimage()
	//printBranches(uint64(*block))
	//execToBlock(*block)
	//extractTrie(*block)
	//fmt.Printf("%x\n", crypto.Keccak256(common.FromHex("0x040c0668aebe0bc41be1f70ebbed671dfdcd118be767a1ad6f78861c5e81abfc")))
	//repair()
	//readAccount()
	//repairCurrent()
	//testMemBolt()
	//fmt.Printf("\u00b3\n")
}
