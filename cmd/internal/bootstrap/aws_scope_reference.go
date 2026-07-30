package bootstrap

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	awsScopeReferencePrefix = "dsc-"
	maxULIDTimestamp        = 1<<48 - 1
)

const lowercaseCrockfordAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

type scopeReferenceGenerator struct {
	mu      sync.Mutex
	now     func() time.Time
	entropy io.Reader

	initialized   bool
	lastTimestamp uint64
	lastEntropy   [10]byte
}

func newScopeReferenceGenerator(now func() time.Time, entropy io.Reader) *scopeReferenceGenerator {
	return &scopeReferenceGenerator{now: now, entropy: entropy}
}

func (generator *scopeReferenceGenerator) Generate() (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()

	now := generator.now()
	unixMilliseconds := now.UnixMilli()
	if unixMilliseconds < 0 || uint64(unixMilliseconds) > maxULIDTimestamp {
		return "", fmt.Errorf("current time %s is outside the ULID timestamp range", now.UTC().Format(time.RFC3339Nano))
	}
	timestamp := uint64(unixMilliseconds)

	if !generator.initialized || timestamp > generator.lastTimestamp {
		var nextEntropy [10]byte
		if _, err := io.ReadFull(generator.entropy, nextEntropy[:]); err != nil {
			return "", fmt.Errorf("unable to read cryptographic entropy for scope reference: %w", err)
		}
		generator.lastTimestamp = timestamp
		generator.lastEntropy = nextEntropy
		generator.initialized = true
	} else {
		timestamp = generator.lastTimestamp
		nextEntropy := generator.lastEntropy
		if incrementScopeReferenceEntropy(&nextEntropy) {
			generator.lastEntropy = nextEntropy
		} else {
			if timestamp == maxULIDTimestamp {
				return "", fmt.Errorf("scope reference entropy and timestamp space are exhausted")
			}
			timestamp++
			if _, err := io.ReadFull(generator.entropy, nextEntropy[:]); err != nil {
				return "", fmt.Errorf("unable to refresh cryptographic entropy for scope reference: %w", err)
			}
			generator.lastTimestamp = timestamp
			generator.lastEntropy = nextEntropy
		}
	}

	var value [16]byte
	binary.BigEndian.PutUint64(value[:8], timestamp<<16)
	copy(value[6:], generator.lastEntropy[:])
	return awsScopeReferencePrefix + encodeLowercaseULID(value), nil
}

func incrementScopeReferenceEntropy(entropy *[10]byte) bool {
	for index := len(entropy) - 1; index >= 0; index-- {
		entropy[index]++
		if entropy[index] != 0 {
			return true
		}
	}
	return false
}

func encodeLowercaseULID(value [16]byte) string {
	encoded := make([]byte, 26)
	for characterIndex := range encoded {
		var characterValue byte
		for bitOffset := 0; bitOffset < 5; bitOffset++ {
			conceptualBit := characterIndex*5 + bitOffset
			characterValue <<= 1
			if conceptualBit < 2 {
				continue
			}
			valueBit := conceptualBit - 2
			characterValue |= (value[valueBit/8] >> (7 - valueBit%8)) & 1
		}
		encoded[characterIndex] = lowercaseCrockfordAlphabet[characterValue]
	}
	return string(encoded)
}

var sharedAWSDeploymentScopeReferenceGenerator = newScopeReferenceGenerator(time.Now, rand.Reader)

var nextAWSDeploymentScopeReference = sharedAWSDeploymentScopeReferenceGenerator.Generate
