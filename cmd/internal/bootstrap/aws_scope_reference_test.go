package bootstrap

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScopeReferenceGenerator(t *testing.T) {
	t.Run("encodes the zero ULID in the exact reference form", func(t *testing.T) {
		generator := newScopeReferenceGenerator(
			func() time.Time { return time.UnixMilli(0) },
			bytes.NewReader(make([]byte, 10)),
		)

		scopeRef, err := generator.Generate()
		if err != nil {
			t.Fatalf("unexpected generation error: %v", err)
		}
		if got, want := scopeRef, "dsc-00000000000000000000000000"; got != want {
			t.Fatalf("scope reference: got %q, want %q", got, want)
		}
	})

	t.Run("is monotonic within the same millisecond", func(t *testing.T) {
		generator := newScopeReferenceGenerator(
			func() time.Time { return time.UnixMilli(1_752_643_200_000) },
			bytes.NewReader(make([]byte, 10)),
		)

		first, err := generator.Generate()
		if err != nil {
			t.Fatalf("unexpected first generation error: %v", err)
		}
		second, err := generator.Generate()
		if err != nil {
			t.Fatalf("unexpected second generation error: %v", err)
		}
		if first >= second {
			t.Fatalf("references are not monotonic: first %q, second %q", first, second)
		}
		if !awsScopeReferencePattern.MatchString(first) || !awsScopeReferencePattern.MatchString(second) {
			t.Fatalf("generated invalid references %q and %q", first, second)
		}
	})

	t.Run("remains monotonic if the clock moves backwards", func(t *testing.T) {
		times := []time.Time{time.UnixMilli(2), time.UnixMilli(1)}
		generator := newScopeReferenceGenerator(
			func() time.Time {
				next := times[0]
				times = times[1:]
				return next
			},
			bytes.NewReader(make([]byte, 10)),
		)

		first, err := generator.Generate()
		if err != nil {
			t.Fatalf("unexpected first generation error: %v", err)
		}
		second, err := generator.Generate()
		if err != nil {
			t.Fatalf("unexpected second generation error: %v", err)
		}
		if first >= second {
			t.Fatalf("references are not monotonic across clock rollback: first %q, second %q", first, second)
		}
	})

	t.Run("fails when secure entropy is unavailable", func(t *testing.T) {
		generator := newScopeReferenceGenerator(
			time.Now,
			&errorReader{err: errors.New("entropy unavailable")},
		)
		_, err := generator.Generate()
		if err == nil || !strings.Contains(err.Error(), "cryptographic entropy") {
			t.Fatalf("expected cryptographic entropy error, got %v", err)
		}
	})

	t.Run("does not commit state when overflow entropy refresh fails", func(t *testing.T) {
		generator := newScopeReferenceGenerator(
			func() time.Time { return time.UnixMilli(5) },
			&errorReader{err: errors.New("entropy unavailable")},
		)
		generator.initialized = true
		generator.lastTimestamp = 5
		for index := range generator.lastEntropy {
			generator.lastEntropy[index] = 0xff
		}
		originalEntropy := generator.lastEntropy

		_, err := generator.Generate()
		if err == nil || !strings.Contains(err.Error(), "refresh cryptographic entropy") {
			t.Fatalf("expected entropy refresh error, got %v", err)
		}
		if generator.lastTimestamp != 5 || generator.lastEntropy != originalEntropy {
			t.Fatalf("generator committed failed overflow state: timestamp %d entropy %x", generator.lastTimestamp, generator.lastEntropy)
		}
	})
}

type errorReader struct {
	err error
}

func (reader *errorReader) Read(buffer []byte) (int, error) {
	return 0, reader.err
}
