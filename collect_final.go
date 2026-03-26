package main
import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/base64"
	"math/big"
	"sync"
	"time"
	"regexp"
)
type Transaction struct {
	Nonce    uint64 `json:"nonce"`
	Value    string `json:"value"`
	Receiver string `json:"receiver"`
	Sender   string `json:"sender"`
	GasPrice uint64 `json:"gasPrice"`
	GasLimit uint64 `json:"gasLimit"`
	Data     string `json:"data,omitempty"`
	ChainID  string `json:"chainID"`
	Version  uint32 `json:"version"`
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
	
	// Try Base64 decode
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
	if bech32 == "" {
		return nil, "", fmt.Errorf("no bech32 address in PEM comment")
	}
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
func send(proxy string, priv ed25519.PrivateKey, from, to string, val *big.Int, nonce uint64) {
	tx := Transaction{
		Nonce: nonce, Value: val.String(), Receiver: to, Sender: from,
		GasPrice: 2000000000, GasLimit: 50000, ChainID: "B", Version: 1,
	}
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	http.Post(proxy + "/transaction/send", "application/json", strings.NewReader(string(payload)))
}
func main() {
	if len(os.Args) < 3 { log.Fatal("Usage: collect <dir> <master_addr>") }
	dir, master := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	pattern := filepath.Join(dir, "*.pem")
	files, _ := filepath.Glob(pattern)
	var wg sync.WaitGroup
	for _, f := range files {
		priv, addr, err := parsePEM(f)
		if err != nil { continue }
		if addr == master || strings.Contains(addr, "0mcwua") { continue }
		wg.Add(1)
		go func(p ed25519.PrivateKey, a string) {
			defer wg.Done()
			bal := getBalance(proxy, a)
			min := new(big.Int).Mul(big.NewInt(2), new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil)) // 0.02
			if bal.Cmp(min) > 0 {
				toSend := new(big.Int).Sub(bal, min)
				nonce := getNonce(proxy, a)
				send(proxy, p, a, master, toSend, nonce)
				fmt.Printf("Collected from %s\n", a)
			}
		}(priv, addr)
		time.Sleep(150 * time.Millisecond)
	}
	wg.Wait()
}
