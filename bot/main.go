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

// ─── CONFIG ──────────────────────────────────────────────────────────────────

type ShardConfig struct {
	ShardID         int
	PEMFile         string
	ContractAddress string
	WalletAddress   string
}

var (
	proxy    = "https://gateway.battleofnodes.com"
	chainID  = "B"
	gasPrice = uint64(1_000_000_000)
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
}

func NewNonceManager(address string) (*NonceManager, error) {
	n, err := fetchNonce(address)
	if err != nil {
		return nil, err
	}
	return &NonceManager{nonce: n}, nil
}

func (nm *NonceManager) Next() uint64 {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	n := nm.nonce
	nm.nonce++
	return n
}

// Reset fetches the current on-chain nonce and resets local state.
// Call this after a nonce error to re-sync.
func (nm *NonceManager) Reset(address string) {
	n, err := fetchNonce(address)
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

// 0.001 WEGLD per call — safe to spam without running out quickly
var txAmount = new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil))

func buildESDTCall(tokenID string, amount *big.Int, fn string) string {
	return fmt.Sprintf("ESDTTransfer@%s@%s@%s", hexEncode(tokenID), amountHex(amount), hexEncode(fn))
}

// ─── NETWORK HELPERS ─────────────────────────────────────────────────────────

func fetchNonce(address string) (uint64, error) {
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

func fetchWEGLDBalance(address, tokenID string) (*big.Int, error) {
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
func dispatchTx(shard ShardConfig, privKey ed25519.PrivateKey, nonce uint64, data string, value *big.Int) (string, error) {
	valueStr := "0"
	if value != nil && value.Sign() > 0 {
		valueStr = value.String()
	}
	tx := NewTx(nonce, shard.WalletAddress, shard.ContractAddress, valueStr, gasPrice, gasLimit, data, chainID, 2)
	return broadcast(proxy, tx, privKey)
}

// ─── SHARD WORKER ────────────────────────────────────────────────────────────

type ShardWorker struct {
	cfg      ShardConfig
	privKey  ed25519.PrivateKey
	nonces   *NonceManager
	txCount  atomic.Int64
	errCount atomic.Int64
	callType string
}

func NewShardWorker(cfg ShardConfig, callType string) (*ShardWorker, error) {
	privKey, _, err := parsePEM(cfg.PEMFile)
	if err != nil {
		return nil, fmt.Errorf("shard%d: parse PEM: %w", cfg.ShardID, err)
	}
	nm, err := NewNonceManager(cfg.WalletAddress)
	if err != nil {
		return nil, fmt.Errorf("shard%d: fetch nonce: %w", cfg.ShardID, err)
	}
	return &ShardWorker{cfg: cfg, privKey: privKey, nonces: nm, callType: callType}, nil
}

// Run fires transactions as fast as possible until ctx is closed.
// interval controls tx cadence; set to 0 for max throughput.
func (w *ShardWorker) Run(ctx chan struct{}, wg *sync.WaitGroup, interval time.Duration) {
	defer wg.Done()
	log.Printf("[Shard%d] Start: callType=%s contract=%s", w.cfg.ShardID, w.callType, w.cfg.ContractAddress)

	if interval == 0 {
		// fire-and-forget goroutine pool
		for {
			select {
			case <-ctx:
				log.Printf("[Shard%d] Stop. TXs=%d Errors=%d", w.cfg.ShardID, w.txCount.Load(), w.errCount.Load())
				return
			default:
				w.sendNext()
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
			go w.sendNext()
		}
	}
}

func (w *ShardWorker) sendNext() {
	data := buildESDTCall(wegldToken, txAmount, w.callType)
	nonce := w.nonces.Next()
	hash, err := dispatchTx(w.cfg, w.privKey, nonce, data, nil)
	if err != nil {
		w.errCount.Add(1)
		errStr := err.Error()
		if strings.Contains(errStr, "nonce too low") || strings.Contains(errStr, "nonce too high") {
			w.nonces.Reset(w.cfg.WalletAddress)
		}
		log.Printf("[Shard%d] ERROR nonce=%d: %v", w.cfg.ShardID, nonce, err)
		return
	}
	w.txCount.Add(1)
	log.Printf("[Shard%d] OK nonce=%d hash=%s", w.cfg.ShardID, nonce, hash)
}

// ─── DRAIN WORKER ────────────────────────────────────────────────────────────

// Cross-shard async calls lock tokens in the contract. Drain releases them.
func DrainWorker(workers []*ShardWorker, interval time.Duration, stop chan struct{}) {
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
				nonce := w.nonces.Next()
				hash, err := dispatchTx(w.cfg, w.privKey, nonce, "drain", nil)
				if err != nil {
					log.Printf("[Drain] Shard%d ERROR: %v", w.cfg.ShardID, err)
					continue
				}
				log.Printf("[Drain] Shard%d drain sent hash=%s", w.cfg.ShardID, hash)
			}
		}
	}
}

// ─── MAIN ────────────────────────────────────────────────────────────────────

func main() {
	durationFlag := flag.Duration("duration", 30*time.Minute, "How long to run the bot")
	callTypeFlag := flag.String("calltype", "auto", "blindSync|blindAsyncV1|blindAsyncV2|blindTransfExec|auto")
	intervalMsFlag := flag.Int("interval", 100, "Milliseconds between TXs per shard (0=max throughput)")
	drainIntervalFlag := flag.Int("drain-interval", 6000, "Milliseconds between drain calls")
	flag.Parse()

	log.Printf("=== BoN Bot | duration=%v callType=%s interval=%dms ===", *durationFlag, *callTypeFlag, *intervalMsFlag)

	shardCallTypes := map[int]string{
		0: "blindAsyncV2",    // cross-shard: async with drain
		1: "blindSync",       // same-shard: tokens return automatically
		2: "blindTransfExec", // cross-shard: transferAndExecute
	}
	if *callTypeFlag != "auto" {
		for k := range shardCallTypes {
			shardCallTypes[k] = *callTypeFlag
		}
	}

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	txInterval := time.Duration(*intervalMsFlag) * time.Millisecond

	var allWorkers []*ShardWorker
	for _, cfg := range shards {
		ct := shardCallTypes[cfg.ShardID]
		w, err := NewShardWorker(cfg, ct)
		if err != nil {
			log.Fatalf("Init shard%d: %v", cfg.ShardID, err)
		}
		allWorkers = append(allWorkers, w)
		wg.Add(1)
		go w.Run(stopCh, &wg, txInterval)
	}

	// Drain worker fires drain() on cross-shard wallets periodically
	go DrainWorker(allWorkers, time.Duration(*drainIntervalFlag)*time.Millisecond, stopCh)

	// Stats every 10 seconds
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			total, errs := int64(0), int64(0)
			for _, w := range allWorkers {
				total += w.txCount.Load()
				errs += w.errCount.Load()
			}
			log.Printf("[Stats] TXs=%d Errors=%d", total, errs)
		}
	}()

	time.Sleep(*durationFlag)
	close(stopCh)
	wg.Wait()

	total := int64(0)
	for _, w := range allWorkers {
		total += w.txCount.Load()
	}
	log.Printf("=== Finished. Total TXs: %d ===", total)
	os.Exit(0)
}
