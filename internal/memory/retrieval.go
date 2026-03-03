package memory

import (
	"context"
	"encoding/binary"
	"math"
	"sort"

	"github.com/hackertron/Yantra/internal/types"
)

// scoredChunk pairs a chunk with a retrieval score.
type scoredChunk struct {
	chunk types.MemoryChunk
	score float64
}

// vectorSearch loads all chunk embeddings, computes cosine similarity against
// the query vector, and returns the top N results. This is a brute-force
// approach that works well for MVP scale (< 10k chunks).
func vectorSearch(ctx context.Context, db *DB, queryVec []float32, topN int) ([]scoredChunk, error) {
	rows, err := db.conn.QueryContext(ctx,
		`SELECT id, content, source, tags, embedding, created_at FROM chunks WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []scoredChunk
	for rows.Next() {
		var id, content, source, tags string
		var embBlob []byte
		var createdAt string
		if err := rows.Scan(&id, &content, &source, &tags, &embBlob, &createdAt); err != nil {
			return nil, err
		}
		emb := decodeFloat32s(embBlob)
		if len(emb) == 0 {
			continue
		}

		sim := cosineSimilarity(queryVec, emb)
		results = append(results, scoredChunk{
			chunk: types.MemoryChunk{
				ID:      id,
				Content: content,
				Source:  source,
				Tags:    parseTags(tags),
			},
			score: sim,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if len(results) > topN {
		results = results[:topN]
	}
	return results, nil
}

// ftsSearch performs a full-text search using SQLite FTS5.
func ftsSearch(ctx context.Context, db *DB, query string, topN int) ([]scoredChunk, error) {
	rows, err := db.conn.QueryContext(ctx,
		`SELECT chunks_fts.id, chunks_fts.content, c.source, c.tags, bm25(chunks_fts) AS rank
		 FROM chunks_fts
		 JOIN chunks c ON c.id = chunks_fts.id
		 WHERE chunks_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`, query, topN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []scoredChunk
	for rows.Next() {
		var id, content, source, tags string
		var rank float64
		if err := rows.Scan(&id, &content, &source, &tags, &rank); err != nil {
			return nil, err
		}
		// BM25 rank is negative (lower = better); convert to positive score.
		results = append(results, scoredChunk{
			chunk: types.MemoryChunk{
				ID:      id,
				Content: content,
				Source:  source,
				Tags:    parseTags(tags),
			},
			score: -rank,
		})
	}
	return results, rows.Err()
}

// reciprocalRankFusion merges vector and FTS results using weighted RRF.
// k=60 is the standard RRF constant.
func reciprocalRankFusion(vectorResults, ftsResults []scoredChunk, vectorWeight, ftsWeight float64, topK int) []scoredChunk {
	const k = 60.0

	scores := make(map[string]float64)
	chunks := make(map[string]types.MemoryChunk)

	for rank, sc := range vectorResults {
		scores[sc.chunk.ID] += vectorWeight / (k + float64(rank+1))
		chunks[sc.chunk.ID] = sc.chunk
	}
	for rank, sc := range ftsResults {
		scores[sc.chunk.ID] += ftsWeight / (k + float64(rank+1))
		chunks[sc.chunk.ID] = sc.chunk
	}

	type idScore struct {
		id    string
		score float64
	}
	var merged []idScore
	for id, score := range scores {
		merged = append(merged, idScore{id, score})
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].score > merged[j].score
	})

	if len(merged) > topK {
		merged = merged[:topK]
	}

	out := make([]scoredChunk, len(merged))
	for i, is := range merged {
		c := chunks[is.id]
		c.Score = is.score
		out[i] = scoredChunk{chunk: c, score: is.score}
	}
	return out
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// encodeFloat32s converts a float32 slice to little-endian bytes for BLOB storage.
func encodeFloat32s(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeFloat32s converts little-endian bytes back to a float32 slice.
func decodeFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// parseTags splits a comma-separated tag string into a slice.
func parseTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			tag := s[start:i]
			if tag != "" {
				tags = append(tags, tag)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		tags = append(tags, s[start:])
	}
	return tags
}

// joinTags joins tags into a comma-separated string.
func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out := tags[0]
	for _, t := range tags[1:] {
		out += "," + t
	}
	return out
}
