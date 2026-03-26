package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Transaction struct {
	Nonce     uint64 `json:"nonce"`
	Value     string `json:"value"`
	Receiver  string `json:"receiver"`
	Sender    string `json:"sender"`
	GasPrice  uint64 `json:"gasPrice"`
	GasLimit  uint64 `json:"gasLimit"`
	ChainID   string `json:"chainID"`
	Version   uint32 `json:"version"`
	Signature string `json:"signature,omitempty"`
}

func parsePEM(file string) (ed25519.PrivateKey, string, error) {
	b, _ := os.ReadFile(file)
	s := string(b)
	lines := strings.Split(s, "\n")
	var raw string
	var bech32 string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "-----") {
			re := regexp.MustCompile("erd1[a-z0-9]{58}")
			if m := re.FindString(t); m != "" { bech32 = m }
			continue
		}
		if t == "" { continue }
		raw += t
	}
	data, _ := base64.StdEncoding.DecodeString(raw)
	var content string
	if data != nil { content = string(data) } else { content = raw }
	re := regexp.MustCompile("[0-9a-fA-F]{64}")
	match := re.FindString(content)
	var seed []byte
	if match != "" { seed, _ = hex.DecodeString(match) } else if len(data) >= 32 { seed = data[:32] }
	if seed == nil { return nil, "", fmt.Errorf("bad key") }
	priv := ed25519.NewKeyFromSeed(seed)
	return priv, bech32, nil
}

func getJSON(url string, target interface{}) error {
	resp, err := http.Get(url)
	if err != nil { return err }
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func getBalance(proxy, addr string) *big.Int {
	var r struct { Data struct { Account struct { Balance string } } }
	if err := getJSON(proxy+"/address/"+addr, &r); err != nil { return big.NewInt(0) }
	b := new(big.Int)
	b.SetString(r.Data.Account.Balance, 10)
	return b
}

func getNonce(proxy, addr string) uint64 {
	var r struct { Data struct { Account struct { Nonce uint64 } } }
	if err := getJSON(proxy+"/address/"+addr, &r); err != nil { return 0 }
	return r.Data.Account.Nonce
}

func send(proxy string, tx *Transaction, priv ed25519.PrivateKey) error {
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	resp, err := http.Post(proxy+"/transaction/send", "application/json", bytes.NewReader(payload))
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { 
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body)) 
	}
	return nil
}

func main() {
	if len(os.Args) < 3 { log.Fatal("Usage: stabilize <master_pem> <final_99_file>") }
	masterPem, final99File := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	
	priv, masterAddr, _ := parsePEM(masterPem)
	targetBal, _ := new(big.Int).SetString("4900000000000000000", 10) // 4.9 EGLD
	fee := big.NewInt(100000000000000) // 0.0001 reserve
	
	activeMap := make(map[string]bool)
	data, _ := os.ReadFile(final99File)
	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if len(t) > 0 { activeMap[t] = true }
	}

	masterNonce := getNonce(proxy, masterAddr)
	wallets := []string{"/root/wallets/shard1", "/root/wallets/shard0", "/root/wallets/shard2"}
	var wg sync.WaitGroup
	var nonceLock sync.Mutex

	for _, dir := range wallets {
		pattern := filepath.Join(dir, "*.pem")
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			p, a, err := parsePEM(f)
			if err != nil || a == masterAddr { continue }
			wg.Add(1)
			go func(privKey ed25519.PrivateKey, addr string) {
				defer wg.Done()
				bal := getBalance(proxy, addr)
				nonce := getNonce(proxy, addr)

				if activeMap[addr] {
					if bal.Cmp(targetBal) < 0 {
						// Top up
						diff := new(big.Int).Sub(targetBal, bal)
						nonceLock.Lock()
						tx := &Transaction{
							Nonce: masterNonce, Value: diff.String(), Receiver: addr, Sender: masterAddr,
							GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
						}
						masterNonce++
						nonceLock.Unlock()
						if err := send(proxy, tx, priv); err == nil {
							fmt.Printf("[%s] Topped up with %s\n", addr, diff.String())
						}
					} else if bal.Cmp(new(big.Int).Add(targetBal, fee)) > 0 {
						// Sweep excess
						excess := new(big.Int).Sub(bal, targetBal)
						excess.Sub(excess, fee)
						tx := &Transaction{
							Nonce: nonce, Value: excess.String(), Receiver: masterAddr, Sender: addr,
							GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
						}
						if err := send(proxy, tx, privKey); err == nil {
							fmt.Printf("[%s] Swept %s excess\n", addr, excess.String())
						}
					}
				} else {
					// Extra: Drain to 0
					if bal.Cmp(fee) > 0 {
						d := new(big.Int).Sub(bal, fee)
						tx := &Transaction{
							Nonce: nonce, Value: d.String(), Receiver: masterAddr, Sender: addr,
							GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
						}
						if err := send(proxy, tx, privKey); err == nil {
							fmt.Printf("[%s] Drained extra wallet\n", addr)
						}
					}
				}
			}(p, a)
			time.Sleep(150 * time.Millisecond)
		}
	}
	wg.Wait()
}
