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
	"path/filepath"
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
	WalletsDir      string
	ContractAddress string
}

var (
	chainID  = "B"
	gasLimit = uint64(10_000_000)

	// identifer of the WEGLD token on BoN network
	wegldToken = "WEGLD-bd4d79"

	shards = []ShardConfig{
		{
			ShardID:         0,
			WalletsDir:      "/root/wallets/shard0",
			ContractAddress: "erd1qqqqqqqqqqqqqpgqhhhyje8gun3r4zuf25x3z3rl6rky0sfac57qw5pjnw",
		},
		{
			ShardID:         1,
			WalletsDir:      "/root/wallets/shard1",
			ContractAddress: "erd1qqqqqqqqqqqqqpgqawj0d0vyeseh2avcg4lfvdsmjtgdxpsh9tdsxwc6ef",
		},
		{
			ShardID:         2,
			WalletsDir:      "/root/wallets/shard2",
			ContractAddress: "erd1qqqqqqqqqqqqqpgqfevad6stfzucutz0xvzw360nwjfqj3q57laqej9a0z",
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

var txAmountWegld = new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil))
var txAmountUsdc = big.NewInt(50000)

func buildESDTCall(tokenID string, amount *big.Int, fn string, expectedToken string) string {
	// erd1qqqqqqqqqqqqqpgqeel2kumf0r8ffyhth7pqdujjat9nx0862jpsg2pqaq (Shard 2 DEX)
	destHex := "00000000000000000500ce7eab736978ce9492ebbf8206f252eacb333cfa5483"
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

func dispatchTx(proxy string, w *ShardWorker, nonce uint64, data string, value *big.Int) (string, error) {
	valueStr := "0"
	if value != nil && value.Sign() > 0 {
		valueStr = value.String()
	}
	gp := baseGasPrice
	if globalTxsSent.Load() < boostLimit {
		gp = boostGasPrice
	}
	tx := NewTx(nonce, w.walletAddr, w.cfg.ContractAddress, valueStr, gp, gasLimit, data, chainID, 2)
	return broadcast(proxy, tx, w.privKey)
}

// ─── SHARD WORKER ────────────────────────────────────────────────────────────

type WorkerRole string
const (
	RoleSpammer WorkerRole = "spammer"
	RoleDrainer WorkerRole = "drainer"
	RoleReserve WorkerRole = "reserve"
)

type ShardWorker struct {
	cfg        ShardConfig
	privKey    ed25519.PrivateKey
	walletAddr string
	nonces     *NonceManager
	txCount    atomic.Int64
	errCount   atomic.Int64
	
	role       WorkerRole
	callTypes  []string
	proxy      string
	batchIdx   uint64
}

func NewShardWorker(proxy string, cfg ShardConfig, pemFile string, role WorkerRole, callTypes []string) (*ShardWorker, error) {
	privKey, walletAddr, err := parsePEM(pemFile)
	if err != nil {
		return nil, fmt.Errorf("shard%d: parse PEM %s: %w", cfg.ShardID, pemFile, err)
	}
	nm, err := NewNonceManager(proxy, walletAddr)
	if err != nil {
		return nil, fmt.Errorf("shard%d: fetch nonce %s: %w", cfg.ShardID, walletAddr, err)
	}
	return &ShardWorker{
		cfg: cfg, privKey: privKey, walletAddr: walletAddr, 
		nonces: nm, role: role, callTypes: callTypes, proxy: proxy,
	}, nil
}

func loadAllShards(proxy string) ([]*ShardWorker, error) {
	var all []*ShardWorker
	
	for _, cfg := range shards {
		entries, _ := filepath.Glob(filepath.Join(cfg.WalletsDir, "*.pem"))
		if len(entries) == 0 {
			return nil, fmt.Errorf("no wallets for shard %d", cfg.ShardID)
		}
		
		// Partition counts according to user spec
		var roles []struct {
			count int
			role  WorkerRole
			ct    []string
		}
		
		if cfg.ShardID == 1 {
			roles = []struct{count int; role WorkerRole; ct []string}{
				{35, RoleSpammer, []string{"blindSync"}},
				{5,  RoleSpammer, []string{"blindAsyncV1"}},
				{5,  RoleSpammer, []string{"blindAsyncV2"}},
				{3,  RoleSpammer, []string{"blindTransfExec"}},
				{2,  RoleDrainer, nil},
			}
		} else { // Shard 0 and 2
			roles = []struct{count int; role WorkerRole; ct []string}{
				{7,  RoleSpammer, []string{"blindAsyncV1"}},
				{7,  RoleSpammer, []string{"blindAsyncV2"}},
				{7,  RoleSpammer, []string{"blindTransfExec"}},
				{3,  RoleDrainer, nil},
				{1,  RoleReserve, nil},
			}
		}
		
		idx := 0
		for _, r := range roles {
			for i := 0; i < r.count; i++ {
				if idx >= len(entries) { break }
				w, err := NewShardWorker(proxy, cfg, entries[idx], r.role, r.ct)
				if err != nil {
					log.Printf("[Shard%d] WARN: skip %s: %v", cfg.ShardID, entries[idx], err)
				} else {
					all = append(all, w)
				}
				idx++
			}
		}
		log.Printf("[Shard%d] Initialized %d workers (Spec applied)", cfg.ShardID, idx)
	}
	return all, nil
}

func (w *ShardWorker) Run(ctx chan struct{}, wg *sync.WaitGroup, interval time.Duration, batchSize int) {
	defer wg.Done()
	if w.role != RoleSpammer {
		return // Drainers and Reserves don't spam
	}

	ticker := time.NewTicker(interval)
	if interval == 0 { ticker.Stop() }
	
	for {
		select {
		case <-ctx:
			return
		default:
			if interval > 0 { <-ticker.C }
			w.sendBatch(batchSize)
		}
	}
}

func (w *ShardWorker) sendBatch(batchSize int) {
	if maxTxsLimit > 0 && globalTxsSent.Load() >= maxTxsLimit {
		return 
	}

	w.batchIdx++
	ct := w.callTypes[w.batchIdx % uint64(len(w.callTypes))]
	
	sendT, expectT, amt := wegldToken, "USDC-c76f1f", txAmountWegld
	if time.Since(appStartTime) > 20*time.Second && w.batchIdx % 2 == 1 {
		sendT, expectT, amt = "USDC-c76f1f", wegldToken, txAmountUsdc
	}

	var txs []*Transaction
	nonces := w.nonces.NextBatch(batchSize)
	data := buildESDTCall(sendT, amt, ct, expectT)
	
	gp := baseGasPrice
	if globalTxsSent.Load() < boostLimit { gp = boostGasPrice }

	for i := 0; i < batchSize; i++ {
		tx := NewTx(nonces[i], w.walletAddr, w.cfg.ContractAddress, "0", gp, gasLimit, data, chainID, 2)
		txs = append(txs, tx)
	}

	hashes, err := bulkBroadcast(w.proxy, txs, w.privKey)
	if err != nil {
		w.errCount.Add(int64(batchSize))
		if strings.Contains(err.Error(), "nonce") { w.nonces.Reset(w.walletAddr) }
		return
	}
	
	globalTxsSent.Add(int64(len(hashes)))
	w.txCount.Add(int64(len(hashes)))
	if w.batchIdx == 1 {
		log.Printf("[Shard%d] Worker %s role=%s first_ct=%s", w.cfg.ShardID, w.walletAddr[:10], w.role, ct)
	}
}

// ─── DRAIN WORKER ────────────────────────────────────────────────────────────

func DrainWorker(proxy string, allWorkers []*ShardWorker, interval time.Duration, stop chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Claim Dev Rewards (use one drainer per shard)
			for _, sid := range []int{0, 1, 2} {
				var target *ShardWorker
				for _, w := range allWorkers {
					if w.cfg.ShardID == sid && w.role == RoleDrainer {
						target = w
						break
					}
				}
				if target == nil { continue }
				
				// 1. Claim Rewards
				n0 := target.nonces.Next()
				dispatchTx(proxy, target, n0, "ClaimDeveloperRewards", nil)
				
				// 2. Drain (if not Shard 1 sync)
				if sid != 1 {
					n1 := target.nonces.Next()
					dispatchTx(proxy, target, n1, "drain@"+hexEncode("USDC-c76f1f"), nil)
					n2 := target.nonces.Next()
					dispatchTx(proxy, target, n2, "drain@"+hexEncode(wegldToken), nil)
				}
			}
		}
	}
}

// ─── MAIN ────────────────────────────────────────────────────────────────────

func main() {
	dur := flag.Duration("duration", 30*time.Minute, "")
	intv := flag.Int("interval", 100, "")
	batch := flag.Int("batch-size", 20, "")
	maxTxs := flag.Int64("max-txs", 0, "")
	proxy := flag.String("proxy", "https://gateway.battleofnodes.com", "")
	gp := flag.Uint64("gas-price", 1_000_000_000, "")
	boostL := flag.Int64("boost-limit", 3000, "")
	boostGP := flag.Uint64("boost-gas-price", 2_000_000_000, "")
	flag.Parse()

	maxTxsLimit, baseGasPrice, boostLimit, boostGasPrice = *maxTxs, *gp, *boostL, *boostGP
	appStartTime = time.Now()

	workers, err := loadAllShards(*proxy)
	if err != nil { log.Fatal(err) }

	stop := make(chan struct{})
	var wg sync.WaitGroup
	txIntv := time.Duration(*intv) * time.Millisecond

	for _, w := range workers {
		wg.Add(1)
		go w.Run(stop, &wg, txIntv, *batch)
	}

	http.HandleFunc("/metrics", func(resp http.ResponseWriter, r *http.Request) {
		for _, w := range workers {
			if w.role == RoleSpammer {
				fmt.Fprintf(resp, "bot_txs_sent_total{shard=\"%d\",addr=\"%s\",ct=\"%s\"} %d\n", w.cfg.ShardID, w.walletAddr[:10], w.callTypes[0], w.txCount.Load())
			}
		}
	})
	go http.ListenAndServe(":2112", nil)
	go DrainWorker(*proxy, workers, 6*time.Second, stop)

	go func() {
		for range time.Tick(5 * time.Second) {
			var t, e int64
			for _, w := range workers { t += w.txCount.Load(); e += w.errCount.Load() }
			log.Printf("[Stats] TXs=%d Errors=%d", t, e)
			if maxTxsLimit > 0 && t >= maxTxsLimit { close(stop); return }
		}
	}()

	select {
	case <-time.After(*dur): close(stop)
	case <-stop:
	}
	wg.Wait()
}
