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
	Version   uint32 `json:"version"`
	Signature string `json:"signature,omitempty"`
}

func parsePEM(file string) (ed25519.PrivateKey, string, error) {
	b, err := os.ReadFile(file)
	if err != nil { return nil, "", err }
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
	data, err := base64.StdEncoding.DecodeString(raw)
	var content string
	if err == nil { content = string(data) } else { content = raw }
	re := regexp.MustCompile("[0-9a-fA-F]{64}")
	match := re.FindString(content)
	var seed []byte
	if match != "" {
		seed, _ = hex.DecodeString(match)
	} else if len(data) >= 32 {
		seed = data[:32]
	} else {
		return nil, "", fmt.Errorf("bad seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if bech32 == "" { return nil, "", fmt.Errorf("no bech32 address") }
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

func getESDTBalance(proxy, addr, token string) *big.Int {
	var r struct { Data struct { TokenData struct { Balance string } } }
	if err := getJSON(proxy+"/address/"+addr+"/esdt/"+token, &r); err != nil { return big.NewInt(0) }
	b := new(big.Int)
	b.SetString(r.Data.TokenData.Balance, 10)
	return b
}

func send(proxy string, tx *Transaction, priv ed25519.PrivateKey) error {
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	resp, err := http.Post(proxy+"/transaction/send", "application/json", bytes.NewReader(payload))
	if err != nil { return err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 { return fmt.Errorf("status %d: %s", resp.StatusCode, string(body)) }
	return nil
}

func main() {
	if len(os.Args) < 3 { log.Fatal("Usage: drain <dir> <master_addr>") }
	dir, master := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	tokens := []string{"WEGLD-bd4d79", "USDC-c76f1f"}
	pattern := filepath.Join(dir, "*.pem")
	files, _ := filepath.Glob(pattern)
	var wg sync.WaitGroup
	for _, f := range files {
		priv, addr, err := parsePEM(f)
		if err != nil || addr == master { continue }
		wg.Add(1)
		go func(p ed25519.PrivateKey, a string) {
			defer wg.Done()
			nonce := getNonce(proxy, a)
			// 1. Drain ESDTs
			for _, t := range tokens {
				bal := getESDTBalance(proxy, a, t)
				if bal.Cmp(big.NewInt(0)) > 0 {
					data := fmt.Sprintf("ESDTTransfer@%s@%s", hex.EncodeToString([]byte(t)), fmt.Sprintf("%x", bal))
					if len(fmt.Sprintf("%x", bal))%2 != 0 { data = fmt.Sprintf("ESDTTransfer@%s@0%x", hex.EncodeToString([]byte(t)), bal) }
					tx := &Transaction{
						Nonce: nonce, Value: "0", Receiver: master, Sender: a,
						GasPrice: 2000000000, GasLimit: 500000, ChainID: "B", Version: 1,
						Data: base64.StdEncoding.EncodeToString([]byte(data)),
					}
					if err := send(proxy, tx, p); err == nil {
						fmt.Printf("[%s] Sent %s %s to %s\n", a, bal.String(), t, master)
						nonce++
					}
				}
			}
			// 2. Drain EGLD (leaving exact 0)
			bal := getBalance(proxy, a)
			fee := big.NewInt(100000000000000) // 0.0001 EGLD for 50k gas at 2G gas price
			if bal.Cmp(fee) > 0 {
				toSend := new(big.Int).Sub(bal, fee)
				tx := &Transaction{
					Nonce: nonce, Value: toSend.String(), Receiver: master, Sender: a,
					GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
				}
				if err := send(proxy, tx, p); err == nil {
					fmt.Printf("[%s] Sent %s EGLD to %s (Targeting 0)\n", a, toSend.String(), master)
				}
			}
		}(priv, addr)
		time.Sleep(200 * time.Millisecond)
	}
	wg.Wait()
}
