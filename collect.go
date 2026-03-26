package main
import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"sync"
	"time"
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
	lines := strings.Split(string(b), "\n")
	var hexSeed string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "-----") || t == "" {
			continue
		}
		hexSeed = t
		break
	}
	seed, _ := hex.DecodeString(hexSeed)
	if len(seed) != 32 { return nil, "", fmt.Errorf("bad seed") }
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return priv, hex.EncodeToString(pub), nil
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
	resp, err := http.Get(proxy + "/address/" + addr)
	if err != nil { return 0 }
	defer resp.Body.Close()
	var r struct { Data struct { Account struct { Nonce uint64 } } }
	json.NewDecoder(resp.Body).Decode(&r)
	return r.Data.Account.Nonce
}
func send(proxy string, priv ed25519.PrivateKey, from, to string, val *big.Int, nonce uint64) {
	tx := Transaction{
		Nonce: nonce, Value: val.String(), Receiver: to, Sender: from,
		GasPrice: 1000000000, GasLimit: 50000, ChainID: "T", Version: 1,
	}
	jb, _ := json.Marshal(tx)
	sig := ed25519.Sign(priv, jb)
	tx.Signature = hex.EncodeToString(sig)
	payload, _ := json.Marshal(tx)
	resp, err := http.Post(proxy + "/transaction/send", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		fmt.Printf(" [!] Post Error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf(" [+] Post Status: %s - Body: %s\n", resp.Status, string(body))
}
func main() {
	if len(os.Args) < 3 { log.Fatal("Usage: collect <dir> <master_addr>") }
	dir, master := os.Args[1], os.Args[2]
	proxy := "https://gateway.battleofnodes.com"
	pattern := filepath.Join(dir, "*.pem")
	files, err := filepath.Glob(pattern)
	if err != nil { log.Fatalf("Glob error: %v", err) }
	fmt.Printf("Found %d files for pattern %s\n", len(files), pattern)
	var wg sync.WaitGroup
	for _, f := range files {
		priv, addr, err := parsePEM(f)
		if err != nil {
			fmt.Printf(" [-] Skip %s: %v\n", f, err)
			continue
		}
		if addr == master || strings.Contains(addr, "0mcwua") { 
			fmt.Printf(" [i] Skip master %s\n", addr)
			continue 
		}
		wg.Add(1)
		go func(p ed25519.PrivateKey, a string, filename string) {
			defer wg.Done()
			bal := getBalance(proxy, a)
			min := new(big.Int).Mul(big.NewInt(2), new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil)) // 0.02
			if bal.Cmp(min) > 0 {
				toSend := new(big.Int).Sub(bal, min)
				nonce := getNonce(proxy, a)
				fmt.Printf(" [>] Sending %s from %s... ", toSend.String(), a)
				send(proxy, p, a, master, toSend, nonce)
			} else {
				fmt.Printf(" [.] Skipping %s (low balance: %s)\n", filename, bal.String())
			}
		}(priv, addr, filepath.Base(f))
		time.Sleep(300 * time.Millisecond)
	}
	wg.Wait()
}
