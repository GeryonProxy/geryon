package benchmarks

import (
	"fmt"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/cache"
	"github.com/GeryonProxy/geryon/internal/pool"
	"github.com/GeryonProxy/geryon/internal/protocol/common"
	"github.com/GeryonProxy/geryon/internal/protocol/postgresql"
	"github.com/GeryonProxy/geryon/internal/tokenizer"
)

// BenchmarkPoolGetPut benchmarks pool get/put operations
func BenchmarkPoolGetPut(b *testing.B) {
	p := pool.NewPool(&pool.Config{
		MaxConnections: 100,
		MinConnections: 10,
	})
	defer p.Close()

	// Pre-warm pool
	for i := 0; i < 10; i++ {
		conn, _ := p.Get()
		p.Put(conn)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := p.Get()
			if err != nil {
				b.Fatal(err)
			}
			p.Put(conn)
		}
	})
}

// BenchmarkCacheGet benchmarks cache get operations
func BenchmarkCacheGet(b *testing.B) {
	store := cache.NewStore(100*1024*1024, 5*time.Minute)
	defer store.StopCleanup()

	// Pre-populate cache
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		data := []byte(fmt.Sprintf("data-%d", i))
		store.Set(key, data, nil, 5*time.Minute)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%10000)
			store.Get(key)
			i++
		}
	})
}

// BenchmarkCacheSet benchmarks cache set operations
func BenchmarkCacheSet(b *testing.B) {
	store := cache.NewStore(100*1024*1024, 5*time.Minute)
	defer store.StopCleanup()

	data := make([]byte, 1024) // 1KB values

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i)
			store.Set(key, data, nil, 5*time.Minute)
			i++
		}
	})
}

// BenchmarkTokenizer benchmarks SQL tokenization
func BenchmarkTokenizer(b *testing.B) {
	queries := []string{
		"SELECT * FROM users WHERE id = 1",
		"SELECT u.name, p.title FROM users u JOIN posts p ON u.id = p.user_id WHERE p.status = 'published'",
		"INSERT INTO users (name, email) VALUES ('John', 'john@example.com')",
		"UPDATE users SET name = 'Jane' WHERE id = 1",
		"DELETE FROM users WHERE id = 1",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			query := queries[i%len(queries)]
			tokenizer.Tokenize(query)
			i++
		}
	})
}

// BenchmarkPGCodec benchmarks PostgreSQL message encoding/decoding
func BenchmarkPGCodec(b *testing.B) {
	codec := postgresql.NewPGCodec()

	// Create a sample query message
	query := "SELECT * FROM users WHERE id = $1"
	msg := &common.Message{
		Type:    'Q',
		Length:  int32(4 + len(query) + 1),
		Payload: []byte(query),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Extract query (simulates parsing)
			codec.ExtractQuery(msg)
		}
	})
}

// BenchmarkMessageBuffer benchmarks buffer operations
func BenchmarkMessageBuffer(b *testing.B) {
	data := make([]byte, 1024)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := common.NewBuffer(data)
			buf.ReadInt32()
			buf.ReadInt32()
			buf.ReadInt32()
		}
	})
}

// BenchmarkRouting benchmarks query routing decisions
func BenchmarkRouting(b *testing.B) {
	queries := []string{
		"SELECT * FROM users",
		"INSERT INTO users VALUES (1, 'test')",
		"UPDATE users SET name = 'test' WHERE id = 1",
		"DELETE FROM users WHERE id = 1",
		"BEGIN",
		"COMMIT",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		query := queries[i%len(queries)]
		isSelect := false
		isWrite := false

		toks := tokenizer.Tokenize(query)
		if len(toks) > 0 {
			switch toks[0].Type {
			case tokenizer.TokenSelect:
				isSelect = true
			case tokenizer.TokenInsert, tokenizer.TokenUpdate, tokenizer.TokenDelete:
				isWrite = true
			}
		}

		_ = isSelect
		_ = isWrite
	}
}

// Result tracking for benchmark reports
type BenchmarkResult struct {
	Name         string
	Operations   int64
	Duration     time.Duration
	NsPerOp      float64
	AllocsPerOp  int64
	BytesPerOp   int64
}

// FormatResults formats benchmark results for reporting
func FormatResults(results []BenchmarkResult) string {
	output := "\n=== Geryon Benchmark Results ===\n\n"
	output += fmt.Sprintf("%-40s %12s %12s %10s %12s\n", "Benchmark", "Ops/sec", "ns/op", "allocs/op", "bytes/op")
	output += fmt.Sprintf("%-40s %12s %12s %10s %12s\n", "---------", "-------", "-----", "---------", "--------")

	for _, r := range results {
		opsPerSec := float64(r.Operations) / r.Duration.Seconds()
		output += fmt.Sprintf("%-40s %12.0f %12.1f %10d %12d\n",
			r.Name, opsPerSec, r.NsPerOp, r.AllocsPerOp, r.BytesPerOp)
	}

	return output
}
