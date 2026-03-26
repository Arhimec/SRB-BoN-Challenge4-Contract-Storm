package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/sha3"
)

const (
	numShards      = 3
	walletsPerShard = 32
)

// MultiversX shard formula: big-endian uint32 of last 4 pubkey bytes % numShards
func shardForPubKey(pubKey []byte) int {
	n := binary.BigEndian.Uint32(pubKey[28:32])
	return int(n % numShards)
}

// bech32 encoding for erd1 addresses
var charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func toWords(data []byte, frombits, tobits int, pad bool) ([]byte, error) {
	acc := 0
	bits := 0
	var result []byte
	maxv := (1 << tobits) - 1
	for _, v := range data {
		acc = (acc << frombits) | int(v)
		bits += frombits
		for bits >= tobits {
			bits -= tobits
			result = append(result, byte((acc>>bits)&maxv))
		}
	}
	if pad && bits > 0 {
		result = append(result, byte((acc<<(tobits-bits))&maxv))
	}
	return result, nil
}

func bech32Polymod(values []byte) uint32 {
	c := uint32(1)
	gen := []uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	for _, v := range values {
		b := c >> 25
		c = (c&0x1ffffff)<<5 ^ uint32(v)
		for i, g := range gen {
			if (b>>i)&1 == 1 {
				c ^= g
			}
		}
	}
	return c
}

func bech32CreateChecksum(hrp string, data []byte) []byte {
	values := append([]byte{}, hrpExpand(hrp)...)
	values = append(values, data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	polymod := bech32Polymod(values) ^ 1
	ret := make([]byte, 6)
	for i := range ret {
		ret[i] = byte((polymod >> (5 * (5 - i))) & 31)
	}
	return ret
}

func hrpExpand(hrp string) []byte {
	var ret []byte
	for _, c := range hrp {
		ret = append(ret, byte(c>>5))
	}
	ret = append(ret, 0)
	for _, c := range hrp {
		ret = append(ret, byte(c&31))
	}
	return ret
}

func encodeBech32(hrp string, data []byte) string {
	words, _ := toWords(data, 8, 5, true)
	combined := append(words, bech32CreateChecksum(hrp, words)...)
	var result []byte
	for _, b := range combined {
		result = append(result, charset[b])
	}
	return hrp + "1" + string(result)
}

// pubKeyToAddress derives the bech32 address from an ed25519 public key.
// MultiversX uses Blake2b-256 of the raw public key bytes as the "raw address".
// For ed25519, the public key IS the raw address (32 bytes).
func pubKeyToAddress(pubKey ed25519.PublicKey) string {
	// MultiversX stores accounts by their 32-byte public key directly
	return encodeBech32("erd", pubKey)
}

// For actual MultiversX, the pubKey hash is via SHA3-256 (keccak-like) but
// in practice for ed25519 wallets the 32-byte pubkey is the address bytes.
// We use keccak256 of the pubkey for address derivation as per MvX spec.
func pubKeyToAddressKeccak(pubKey ed25519.PublicKey) string {
	h := sha3.NewLegacyKeccak256()
	h.Write(pubKey)
	hash := h.Sum(nil)
	return encodeBech32("erd", hash)
}

func writePEM(path string, privKey ed25519.PrivateKey, addr string) error {
	// MultiversX PEM format: base64 of hex-encoded 64-byte private key seed
	seed := privKey.Seed()
	hexSeed := hex.EncodeToString(seed)
	b64 := base64.StdEncoding.EncodeToString([]byte(hexSeed))

	// Wrap at 64 chars
	var wrapped string
	for i := 0; i < len(b64); i += 64 {
		end := i + 64
		if end > len(b64) {
			end = len(b64)
		}
		wrapped += b64[i:end] + "\n"
	}

	content := fmt.Sprintf("-----BEGIN PRIVATE KEY for %s-----\n%s-----END PRIVATE KEY for %s-----\n",
		addr, wrapped, addr)
	return os.WriteFile(path, []byte(content), 0600)
}

func main() {
	for shard := 0; shard < numShards; shard++ {
		dir := fmt.Sprintf("/root/wallets/shard%d", shard)
		os.MkdirAll(dir, 0700)

		generated := 0
		attempt := 0
		for generated < walletsPerShard {
			attempt++
			pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				panic(err)
			}

			assignedShard := shardForPubKey(pubKey)
			if assignedShard != shard {
				continue
			}

			addr := encodeBech32("erd", pubKey)
			filename := filepath.Join(dir, fmt.Sprintf("wallet%02d.pem", generated+1))
			if err := writePEM(filename, privKey, addr); err != nil {
				panic(err)
			}
			generated++
			fmt.Printf("[Shard%d] %02d/%d  addr=%s  (attempt %d)\n", shard, generated, walletsPerShard, addr, attempt)
		}
		fmt.Printf("✅ Shard%d: generated %d wallets → %s\n\n", shard, walletsPerShard, dir)
	}
	fmt.Println("🎉 All wallets generated!")
}
