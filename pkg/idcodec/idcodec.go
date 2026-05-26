package idcodec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"github.com/sqids/sqids-go"
	"golang.org/x/crypto/hkdf"
)

const (
	baseAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	hkdfInfo     = "nezha/idcodec/alphabet/v1"
	minLength    = 8
	minMasterKey = 32
)

var (
	ErrNotInitialized = errors.New("idcodec: not initialized")
	ErrInvalidCode    = errors.New("idcodec: invalid id code")
	ErrMasterKeyShort = errors.New("idcodec: master key too short")

	mu      sync.RWMutex
	encoder *sqids.Sqids
)

func Init(masterKey []byte) error {
	if len(masterKey) < minMasterKey {
		return ErrMasterKeyShort
	}
	alphaKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, masterKey, nil, []byte(hkdfInfo)), alphaKey); err != nil {
		return err
	}
	enc, err := sqids.New(sqids.Options{
		Alphabet:  keyedShuffle(baseAlphabet, alphaKey),
		MinLength: minLength,
		Blocklist: []string{},
	})
	if err != nil {
		return err
	}
	mu.Lock()
	encoder = enc
	mu.Unlock()
	return nil
}

func Encode(id uint64) (string, error) {
	mu.RLock()
	enc := encoder
	mu.RUnlock()
	if enc == nil {
		return "", ErrNotInitialized
	}
	return enc.Encode([]uint64{id})
}

func Decode(code string) (uint64, error) {
	mu.RLock()
	enc := encoder
	mu.RUnlock()
	if enc == nil {
		return 0, ErrNotInitialized
	}
	nums := enc.Decode(code)
	if len(nums) != 1 {
		return 0, ErrInvalidCode
	}
	if got, err := enc.Encode(nums); err != nil || got != code {
		return 0, ErrInvalidCode
	}
	return nums[0], nil
}

func keyedShuffle(alphabet string, key []byte) string {
	runes := []rune(alphabet)
	mac := hmac.New(sha256.New, key)
	var counter uint64
	var pool []byte
	next := func() byte {
		if len(pool) == 0 {
			buf := make([]byte, 8)
			binary.BigEndian.PutUint64(buf, counter)
			counter++
			mac.Reset()
			mac.Write(buf)
			pool = mac.Sum(nil)
		}
		b := pool[0]
		pool = pool[1:]
		return b
	}
	for i := len(runes) - 1; i > 0; i-- {
		j := int(next()) % (i + 1)
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
