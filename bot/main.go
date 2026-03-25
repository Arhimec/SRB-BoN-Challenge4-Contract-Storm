package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	globalTxsSent atomic.Int64
	maxTxsLimit   int64
	appStartTime  time.Time
	
	baseGasPrice  uint64
	boostGasPrice uint64
	boostLimit    int64
)

// ─── CONFIG ──────────────────────────────────────────────────────────────────

type ShardConfig struct {
	ShardID         int
	PEMFile         string
	ContractAddress string
	WalletAddress   string
}

var (
	chainID  = "B"
	gasLimit = uint64(10_000_000)

	// identifer of the WEGLD token on BoN network
	wegldToken = "WEGLD-bd4d79"

	shards = []ShardConfig{
		{
			ShardID:         0,
			PEMFile:         "/root/wallets/shard0.pem",
			ContractAddress: "erd1qqqqqqqqqqqqqpgqhhhyje8gun3r4zuf25x3z3rl6rky0sfac57qw5pjnw",
			WalletAddress:   "erd12k35xfk0k6en6rfzhfjtvespsm73vhwd3zy4acnwyjlqvrw3c57qtj0rex",
		},
		{
			ShardID:         1,
			PEMFile:         "/root/wallets/shard1.pem",
			ContractAddress: "erd1qqqqqqqqqqqqqpgqawj0d0vyeseh2avcg4lfvdsmjtgdxpsh9tdsxwc6ef",
			WalletAddress:   "erd17dgrw3udskg4q07wharx938nrssrr00722d8dc8xtml5dmuq9tdstjq33k",
		},
		{
			ShardID:         2,
			PEMFile:         "/root/wallets/shard2.pem",
			ContractAddress: "erd1qqqqqqqqqqqqqpgqfevad6stfzucutz0xvzw360nwjfqj3q57laqej9a0z",
			WalletAddress:   "erd1fcdnxc2qklv9c0dh6thr7gzaq90f4r5se6s0alfd0aj6emzn7laqumqzzp",
		},
	}
)

// ─── NONCE MANAGER ───────────────────────────────────────────────────────────

type NonceManager struct {
	mu    sync.Mutex
	nonce uint64
	proxy string
}

func NewNonceManager(proxy, address string) (*NonceManager, error) {
	n, err := fetchNonce(proxy, address)
	if err != nil {
		return nil, err
	}
	return &NonceManager{nonce: n, proxy: proxy}, nil
}

func (nm *NonceManager) Next() uint64 {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	n := nm.nonce
	nm.nonce++
	return n
}

func (nm *NonceManager) NextBatch(size int) []uint64 {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	res := make([]uint64, size)
	for i := 0; i < size; i++ {
		res[i] = nm.nonce
		nm.nonce++
	}
	return res
}

// Reset fetches the current on-chain nonce and resets local state.
// Call this after a nonce error to re-sync.
func (nm *NonceManager) Reset(address string) {
	n, err := fetchNonce(nm.proxy, address)
	if err != nil {
		log.Printf("NonceManager.Reset: %v", err)
		return
	}
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.nonce = n
	log.Printf("NonceManager.Reset: synced to %d for %s", n, address)
}

// ─── TX BUILDER ──────────────────────────────────────────────────────────────

func hexEncode(s string) string { return hex.EncodeToString([]byte(s)) }

func amountHex(amount *big.Int) string {
	h := fmt.Sprintf("%x", amount)
	if len(h)%2 != 0 {
		h = "0" + h
	}
	return h
}

// 0.001 WEGLD per call
var txAmountWegld = new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil))
// 0.05 USDC per call (50000 units of 6 decimals)
var txAmountUsdc = big.NewInt(50000)

func buildESDTCall(tokenID string, amount *big.Int, fn string, expectedToken string) string {
	destHex := "0000000000000000050019e6ab48171f0381c319cb2ccd108d5ca08ee19c0001" // Target DEX pair
	endpointHex := hexEncode("swapTokensFixedInput")
	argTokenHex := hexEncode(expectedToken)
	argAmountHex := "01"

	return fmt.Sprintf("ESDTTransfer@%s@%s@%s@%s@%s@%s@%s", 
		hexEncode(tokenID), amountHex(amount), hexEncode(fn),
		destHex, endpointHex, argTokenHex, argAmountHex)
}

// ─── NETWORK HELPERS ─────────────────────────────────────────────────────────

func fetchNonce(proxy, address string) (uint64, error) {
	resp, err := http.Get(fmt.Sprintf("%s/address/%s", proxy, address))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data struct {
			Account struct {
				Nonce uint64 `json:"nonce"`
			} `json:"account"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	return result.Data.Account.Nonce, nil
}

func fetchWEGLDBalance(proxy, address, tokenID string) (*big.Int, error) {
	url := fmt.Sprintf("%s/address/%s/esdt/%s", proxy, address, tokenID)
	resp, err := http.Get(url)
	if err != nil {
		return big.NewInt(0), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data struct {
			TokenData struct {
				Balance string `json:"balance"`
			} `json:"tokenData"`
		} `json:"data"`
	}
	json.Unmarshal(body, &result)
	bal := new(big.Int)
	bal.SetString(result.Data.TokenData.Balance, 10)
	return bal, nil
}

// dispatchTx builds, signs, and broadcasts a single transaction.
func dispatchTx(proxy string, shard ShardConfig, privKey ed25519.PrivateKey, nonce uint64, data string, value *big.Int) (string, error) {
	valueStr := "0"
	if value != nil && value.Sign() > 0 {
		valueStr = value.String()
	}
	
	gp := baseGasPrice
	if globalTxsSent.Load() < boostLimit {
		gp = boostGasPrice
	}
	
	tx := NewTx(nonce, shard.WalletAddress, shard.ContractAddress, valueStr, gp, gasLimit, data, chainID, 2)
	return broadcast(proxy, tx, privKey)
}

// ─── SHARD WORKER ────────────────────────────────────────────────────────────

type ShardWorker struct {
	cfg      ShardConfig
	privKey  ed25519.PrivateKey
	nonces   *NonceManager
	txCount  atomic.Int64
	errCount atomic.Int64
	callTypes []string
	batchIndex uint64
	proxy string
}

func NewShardWorker(proxy string, cfg ShardConfig, callTypes []string) (*ShardWorker, error) {
	privKey, _, err := parsePEM(cfg.PEMFile)
	if err != nil {
		return nil, fmt.Errorf("shard%d: parse PEM: %w", cfg.ShardID, err)
	}
	nm, err := NewNonceManager(proxy, cfg.WalletAddress)
	if err != nil {
		return nil, fmt.Errorf("shard%d: fetch nonce: %w", cfg.ShardID, err)
	}
	return &ShardWorker{cfg: cfg, privKey: privKey, nonces: nm, callTypes: callTypes, proxy: proxy}, nil
}

// Run fires transactions as fast as possible until ctx is closed.
// interval controls tx cadence; set to 0 for max throughput.
func (w *ShardWorker) Run(ctx chan struct{}, wg *sync.WaitGroup, interval time.Duration, batchSize int) {
	defer wg.Done()
	log.Printf("[Shard%d] Start: callTypes=%v contract=%s batchSize=%d", w.cfg.ShardID, w.callTypes, w.cfg.ContractAddress, batchSize)

	if interval == 0 {
		// fire-and-forget goroutine pool
		for {
			select {
			case <-ctx:
				log.Printf("[Shard%d] Stop. TXs=%d Errors=%d", w.cfg.ShardID, w.txCount.Load(), w.errCount.Load())
				return
			default:
				w.sendBatch(batchSize)
			}
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx:
			log.Printf("[Shard%d] Stop. TXs=%d Errors=%d", w.cfg.ShardID, w.txCount.Load(), w.errCount.Load())
			return
		case <-ticker.C:
			go w.sendBatch(batchSize)
		}
	}
}

func (w *ShardWorker) sendBatch(batchSize int) {
	if maxTxsLimit > 0 && globalTxsSent.Load() >= maxTxsLimit {
		return // Do not send if max txs explicitly reached
	}

	w.batchIndex++
	ct := w.callTypes[w.batchIndex % uint64(len(w.callTypes))]
	
	sendToken, expectToken, amt := wegldToken, "USDC-c76f1f", txAmountWegld
	// AUTOMATED WARMUP: strictly use WEGLD for the first 20 seconds to guarantee USDC balance accrual
	if time.Since(appStartTime) > 20*time.Second && w.batchIndex % 2 == 1 {
		sendToken, expectToken, amt = "USDC-c76f1f", wegldToken, txAmountUsdc
	}

	var txs []*Transaction
	nonces := w.nonces.NextBatch(batchSize)
	data := buildESDTCall(sendToken, amt, ct, expectToken)
	
	gp := baseGasPrice
	if globalTxsSent.Load() < boostLimit {
		gp = boostGasPrice
	}

	for i := 0; i < batchSize; i++ {
		tx := NewTx(nonces[i], w.cfg.WalletAddress, w.cfg.ContractAddress, "0", gp, gasLimit, data, chainID, 2)
		txs = append(txs, tx)
	}

	hashes, err := bulkBroadcast(w.proxy, txs, w.privKey)
	if err != nil {
		w.errCount.Add(int64(batchSize))
		errStr := err.Error()
		if strings.Contains(errStr, "nonce too low") || strings.Contains(errStr, "nonce too high") {
			w.nonces.Reset(w.cfg.WalletAddress)
		}
		log.Printf("[Shard%d] ERROR batch %d txs: %v", w.cfg.ShardID, batchSize, err)
		return
	}
	
	globalTxsSent.Add(int64(len(hashes)))
	w.txCount.Add(int64(len(hashes)))
	if len(hashes) > 0 {
		log.Printf("[Shard%d] OK batch sent %d txs (first nonce=%d, hash=%s, ct=%s, gp=%d, expects=%s)", w.cfg.ShardID, len(hashes), nonces[0], hashes[0], ct, gp, expectToken)
	}
}

// ─── DRAIN WORKER ────────────────────────────────────────────────────────────

// Cross-shard async calls lock tokens in the contract. Drain releases them.
func DrainWorker(proxy string, workers []*ShardWorker, interval time.Duration, stop chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			for _, w := range workers {
				if w.cfg.ShardID == 1 {
					continue // same-shard: tokens return automatically
				}
				// Drain USDC
				nonce1 := w.nonces.Next()
				drainUSDC := fmt.Sprintf("drain@%s", hexEncode("USDC-c76f1f"))
				hash1, err1 := dispatchTx(proxy, w.cfg, w.privKey, nonce1, drainUSDC, nil)
				if err1 != nil {
					log.Printf("[Drain] Shard%d USDC ERROR: %v", w.cfg.ShardID, err1)
				} else {
					log.Printf("[Drain] Shard%d drain USDC sent hash=%s", w.cfg.ShardID, hash1)
				}

				// Drain WEGLD
				nonce2 := w.nonces.Next()
				drainWEGLD := fmt.Sprintf("drain@%s", hexEncode(wegldToken))
				hash2, err2 := dispatchTx(proxy, w.cfg, w.privKey, nonce2, drainWEGLD, nil)
				if err2 != nil {
					log.Printf("[Drain] Shard%d WEGLD ERROR: %v", w.cfg.ShardID, err2)
				} else {
					log.Printf("[Drain] Shard%d drain WEGLD sent hash=%s", w.cfg.ShardID, hash2)
				}
			}
		}
	}
}

// ─── MAIN ────────────────────────────────────────────────────────────────────

func main() {
	durationFlag := flag.Duration("duration", 30*time.Minute, "How long to run the bot")
	callTypeFlag := flag.String("calltype", "auto", "blindSync|blindAsyncV1|blindAsyncV2|blindTransfExec|auto")
	intervalMsFlag := flag.Int("interval", 100, "Milliseconds between TX batches per shard (0=max throughput)")
	drainIntervalFlag := flag.Int("drain-interval", 6000, "Milliseconds between drain calls")
	batchSizeFlag := flag.Int("batch-size", 20, "Number of TXs to send in a single bulkBroadcast array")
	maxTxsFlag := flag.Int64("max-txs", 0, "Stop the bot after reaching this global total of dispatched TXs to preserve gas budget")
	proxyFlag := flag.String("proxy", "https://gateway.battleofnodes.com", "RPC proxy URL")
	
	gasPriceFlag := flag.Uint64("gas-price", 1_000_000_000, "Base gas price for all transactions")
	boostLimitFlag := flag.Int64("boost-limit", 3000, "Number of initial transactions to apply boost-gas-price to")
	boostGasPriceFlag := flag.Uint64("boost-gas-price", 2_000_000_000, "Elevated gas price for the first N transactions to secure the milestone bonus")
	flag.Parse()

	maxTxsLimit = *maxTxsFlag
	baseGasPrice = *gasPriceFlag
	boostLimit = *boostLimitFlag
	boostGasPrice = *boostGasPriceFlag
	appStartTime = time.Now()

	log.Printf("=== BoN Bot | dur=%v ct=%s proxy=%s maxTxs=%d gp=%d boostLim=%d boostGP=%d ===", 
		*durationFlag, *callTypeFlag, *proxyFlag, maxTxsLimit, baseGasPrice, boostLimit, boostGasPrice)

	shardCallTypes := map[int][]string{
		0: {"blindAsyncV1", "blindAsyncV2"}, // cross-shard: 2 types
		1: {"blindSync"},                    // same-shard: 1 type
		2: {"blindTransfExec"},              // cross-shard: 1 type
	}
	if *callTypeFlag != "auto" {
		for k := range shardCallTypes {
			shardCallTypes[k] = []string{*callTypeFlag}
		}
	}

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	txInterval := time.Duration(*intervalMsFlag) * time.Millisecond

	var allWorkers []*ShardWorker
	for _, cfg := range shards {
		ct := shardCallTypes[cfg.ShardID]
		w, err := NewShardWorker(*proxyFlag, cfg, ct)
		if err != nil {
			log.Fatalf("Init shard%d: %v", cfg.ShardID, err)
		}
		allWorkers = append(allWorkers, w)
		wg.Add(1)
		go w.Run(stopCh, &wg, txInterval, *batchSizeFlag)
	}

	// Start Prometheus metrics server on :2112
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		for _, worker := range allWorkers {
			cTypes := strings.Join(worker.callTypes, ",")
			fmt.Fprintf(w, "bot_txs_sent_total{shard=\"%d\",call_type=\"%s\"} %d\n", worker.cfg.ShardID, cTypes, worker.txCount.Load())
			fmt.Fprintf(w, "bot_txs_error_total{shard=\"%d\",call_type=\"%s\"} %d\n", worker.cfg.ShardID, cTypes, worker.errCount.Load())
		}
	})
	go func() {
		log.Println("Metrics available on :2112/metrics")
		if err := http.ListenAndServe(":2112", nil); err != nil {
			log.Printf("Metrics server error: %v", err)
		}
	}()

	// Drain worker fires drain() on cross-shard wallets periodically
	go DrainWorker(*proxyFlag, allWorkers, time.Duration(*drainIntervalFlag)*time.Millisecond, stopCh)

	// Stats every 10 seconds and max limit enforcer
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for range t.C {
			total, errs := int64(0), int64(0)
			for _, w := range allWorkers {
				total += w.txCount.Load()
				errs += w.errCount.Load()
			}
			log.Printf("[Stats] TXs=%d Errors=%d", total, errs)
			if maxTxsLimit > 0 && total >= maxTxsLimit {
				log.Printf("!!! MAX TX LIMIT %d REACHED, HALTING !!!", maxTxsLimit)
				close(stopCh)
				return
			}
		}
	}()

	select {
	case <-time.After(*durationFlag):
		log.Println("Duration completed.")
		close(stopCh)
	case <-stopCh:
		// already closed by limit enforcer
	}

	wg.Wait()

	total := int64(0)
	for _, w := range allWorkers {
		total += w.txCount.Load()
	}
	log.Printf("=== Finished. Total TXs: %d ===", total)
	os.Exit(0)
}
