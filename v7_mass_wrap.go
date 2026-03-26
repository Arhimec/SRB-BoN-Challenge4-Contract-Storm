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
	Data      string `json:"data,omitempty"`
	ChainID   string `json:"chainID"`
	Version   int    `json:"version"`
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
	return ed25519.NewKeyFromSeed(seed), bech32, nil
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

func send(proxy string, tx *Transaction, priv ed25519.PrivateKey) (string, error) {
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	resp, err := http.Post(proxy+"/transaction/send", "application/json", bytes.NewReader(payload))
	if err != nil { return "", err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 { 
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body)) 
	}
	var r struct { Data struct { TxHash string `json:"txHash"` } `json:"data"` }
	json.Unmarshal(body, &r)
	return r.Data.TxHash, nil
}

func main() {
	if len(os.Args) < 2 { log.Fatal("Usage: wrap <shard_dir>") }
	shardDir := os.Args[1]
	proxy := "https://gateway.battleofnodes.com"
	wrapAddr := "erd1qqqqqqqqqqqqqpgq06p3skvclatnvpvg0re7z5pj60u8m9gggs2qmzlazk"
	wrapValue := "3500000000000000000" // 3.5 EGLD
	
	pattern := filepath.Join(shardDir, "*.pem")
	files, _ := filepath.Glob(pattern)
	
	var wg sync.WaitGroup
	for _, f := range files {
		p, a, err := parsePEM(f)
		if err != nil { continue }
		
		wg.Add(1)
		go func(priv ed25519.PrivateKey, addr string) {
			defer wg.Done()
			nonce := getNonce(proxy, addr)
			tx := &Transaction{
				Nonce: nonce, Value: wrapValue, Receiver: wrapAddr, Sender: addr,
				GasPrice: 2000000000, GasLimit: 10000000, ChainID: "B", Version: 2,
				Data: base64.StdEncoding.EncodeToString([]byte("wrapEgld")),
			}
			if txHash, err := send(proxy, tx, priv); err == nil {
				fmt.Printf("[%s] WRAPPED 3.5 EGLD -> WEGLD | TxHash: %s\n", addr, txHash)
			} else {
				fmt.Printf("[%s] WRAP ERROR: %v\n", addr, err)
			}
		}(p, a)
		time.Sleep(200 * time.Millisecond)
	}
	wg.Wait()
}
