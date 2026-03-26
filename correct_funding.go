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

func getBalance(proxy, addr string) *big.Int {
	resp, err := http.Get(proxy + "/address/" + addr)
	if err != nil { return big.NewInt(0) }
	defer resp.Body.Close()
	var r struct { Data struct { Account struct { Balance string } } }
	json.NewDecoder(resp.Body).Decode(&r)
	b := new(big.Int)
	b.SetString(r.Data.Account.Balance, 10)
	return b
}

func getNonce(proxy, addr string) uint64 {
	resp, _ := http.Get(proxy + "/address/" + addr)
	defer resp.Body.Close()
	var r struct { Data struct { Account struct { Nonce uint64 } } }
	json.NewDecoder(resp.Body).Decode(&r)
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
	if len(os.Args) < 2 { log.Fatal("Usage: correct <master_addr>") }
	master := os.Args[1]
	proxy := "https://gateway.battleofnodes.com"
	
	// We have 114 targets. To cap at 500, we target 4.38 EGLD per target.
	// 114 * 4.38 = 499.32 (Safe)
	targetBal, _ := new(big.Int).SetString("4380000000000000000", 10)
	fee := big.NewInt(100000000000000) // 0.0001 reserve for gas of THIS transaction
    
	wallets := []string{"/root/wallets/shard1", "/root/wallets/shard0", "/root/wallets/shard2"}
	var wg sync.WaitGroup
	for _, dir := range wallets {
		pattern := filepath.Join(dir, "*.pem")
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			priv, addr, err := parsePEM(f)
			if err != nil || addr == master { continue }
			wg.Add(1)
			go func(p ed25519.PrivateKey, a string) {
				defer wg.Done()
				bal := getBalance(proxy, a)
				if bal.Cmp(new(big.Int).Add(targetBal, fee)) > 0 {
					toSend := new(big.Int).Sub(bal, targetBal)
                    toSend.Sub(toSend, fee) // Account for current tx fee
					nonce := getNonce(proxy, a)
					tx := &Transaction{
						Nonce: nonce, Value: toSend.String(), Receiver: master, Sender: a,
						GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
					}
					if err := send(proxy, tx, p); err == nil {
						fmt.Printf("[%s] Corrected: sent %s excess to master\n", a, toSend.String())
					} else {
						fmt.Printf("[%s] Error: %v\n", a, err)
					}
				}
			}(priv, addr)
			time.Sleep(150 * time.Millisecond)
		}
	}
	wg.Wait()
}
