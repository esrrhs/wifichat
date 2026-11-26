package fec

import (
	"errors"
	"fmt"
)

const (
	// MaxK is the maximum number of source chunks in a single generation.
	// With seq in 0..255, K <= 64 leaves 192..255 exact Cauchy MDS repair chunks
	// per generation, guaranteeing 100% invertibility for ANY K distinct seqs.
	MaxK = 64
)

var (
	ErrInvalidParams = errors.New("fec: invalid k or chunkSize")
	ErrChunkLength   = errors.New("fec: chunk size mismatch")
)

// cauchyRow fills coeffs (len k) for sequence index seq in [0, 255].
// seq in [0, k) is systematic (unit vector e_seq).
// seq in [k, 255] uses Cauchy entry 1 / (x_p ^ y_j) with disjoint sets
// X = {0 .. 255-k} and Y = {256-k .. 255}, ensuring every KxK submatrix is MDS.
func cauchyRow(k int, seq uint8, coeffs []uint8) {
	for i := range coeffs {
		coeffs[i] = 0
	}
	if int(seq) < k {
		coeffs[seq] = 1
		return
	}
	x := uint8(int(seq) - k)
	baseY := 256 - k
	for j := 0; j < k; j++ {
		y := uint8(baseY + j)
		coeffs[j] = gfInv(x ^ y)
	}
}

// Encoder generates systematic and Cauchy MDS parity chunks for one generation.
type Encoder struct {
	k         int
	chunkSize int
	chunks    [][]byte
}

// NewEncoder splits data into K = ceil(max(1, len(data)) / chunkSize) chunks
// (padded with trailing zeros on the final chunk). K must be <= MaxK.
func NewEncoder(data []byte, chunkSize int) (*Encoder, error) {
	if chunkSize <= 0 {
		return nil, ErrInvalidParams
	}
	k := (len(data) + chunkSize - 1) / chunkSize
	if k == 0 {
		k = 1
	}
	if k > MaxK {
		return nil, fmt.Errorf("%w: k=%d exceeds MaxK=%d", ErrInvalidParams, k, MaxK)
	}
	chunks := make([][]byte, k)
	for i := 0; i < k; i++ {
		ch := make([]byte, chunkSize)
		start := i * chunkSize
		if start < len(data) {
			end := start + chunkSize
			if end > len(data) {
				end = len(data)
			}
			copy(ch, data[start:end])
		}
		chunks[i] = ch
	}
	return &Encoder{k: k, chunkSize: chunkSize, chunks: chunks}, nil
}

func (e *Encoder) K() int         { return e.k }
func (e *Encoder) ChunkSize() int { return e.chunkSize }

// Encode produces the chunk for sequence number seq (0..255).
// seq < K returns systematic chunk seq; seq >= K returns an MDS parity chunk.
func (e *Encoder) Encode(seq uint8) []byte {
	out := make([]byte, e.chunkSize)
	if int(seq) < e.k {
		copy(out, e.chunks[seq])
		return out
	}
	var coeffs [MaxK]uint8
	cauchyRow(e.k, seq, coeffs[:e.k])
	for j := 0; j < e.k; j++ {
		gfMulAddSlice(out, e.chunks[j], coeffs[j])
	}
	return out
}

// Decoder incrementally solves a K-chunk generation via Gauss-Jordan
// elimination in GF(256) as systematic or parity chunks arrive in any order.
type Decoder struct {
	k         int
	chunkSize int
	pivots    [][]uint8 // len k; pivots[col] has 1 at col and 0 at all other active pivot cols
	values    [][]byte  // len k; RHS data row corresponding to pivots[col]
	seen      [256]bool
	rank      int
	repairs   int
}

func NewDecoder(k, chunkSize int) (*Decoder, error) {
	if k <= 0 || k > MaxK || chunkSize <= 0 {
		return nil, ErrInvalidParams
	}
	return &Decoder{
		k:         k,
		chunkSize: chunkSize,
		pivots:    make([][]uint8, k),
		values:    make([][]byte, k),
	}, nil
}

func (d *Decoder) K() int          { return d.k }
func (d *Decoder) Rank() int       { return d.rank }
func (d *Decoder) Needed() int     { return d.k - d.rank }
func (d *Decoder) Complete() bool  { return d.rank == d.k }
func (d *Decoder) RepairsUsed() int { return d.repairs }

// Add inserts chunk seq. Returns (complete, newlyUseful, err).
func (d *Decoder) Add(seq uint8, chunk []byte) (bool, bool, error) {
	if len(chunk) != d.chunkSize {
		return false, false, ErrChunkLength
	}
	if d.seen[seq] {
		return d.rank == d.k, false, nil
	}
	d.seen[seq] = true
	if d.rank == d.k {
		return true, false, nil
	}

	row := make([]uint8, d.k)
	cauchyRow(d.k, seq, row)
	val := make([]byte, d.chunkSize)
	copy(val, chunk)

	// Step 1: Eliminate all columns that already have pivots.
	for c := 0; c < d.k; c++ {
		if d.pivots[c] == nil || row[c] == 0 {
			continue
		}
		f := row[c]
		gfMulAddSlice(row, d.pivots[c], f)
		gfMulAddSlice(val, d.values[c], f)
	}

	// Step 2: Find first non-zero column for the new pivot.
	pivotCol := -1
	for c := 0; c < d.k; c++ {
		if row[c] != 0 {
			pivotCol = c
			break
		}
	}
	if pivotCol < 0 {
		// Mathematically impossible for distinct seqs with Cauchy MDS,
		// but handled defensively.
		return d.rank == d.k, false, nil
	}

	// Step 3: Normalize new pivot row so row[pivotCol] == 1.
	inv := gfInv(row[pivotCol])
	gfScaleSlice(row, inv)
	gfScaleSlice(val, inv)

	// Step 4: Eliminate pivotCol from all previously stored pivot rows
	// to preserve reduced row-echelon form.
	for c := 0; c < d.k; c++ {
		if d.pivots[c] == nil || d.pivots[c][pivotCol] == 0 {
			continue
		}
		f := d.pivots[c][pivotCol]
		gfMulAddSlice(d.pivots[c], row, f)
		gfMulAddSlice(d.values[c], val, f)
	}

	d.pivots[pivotCol] = row
	d.values[pivotCol] = val
	d.rank++
	if int(seq) >= d.k {
		d.repairs++
	}
	return d.rank == d.k, true, nil
}

// Bytes returns the concatenated K * chunkSize reconstructed bytes once Complete().
func (d *Decoder) Bytes() ([]byte, error) {
	if d.rank < d.k {
		return nil, errors.New("fec: generation incomplete")
	}
	out := make([]byte, d.k*d.chunkSize)
	for i := 0; i < d.k; i++ {
		copy(out[i*d.chunkSize:(i+1)*d.chunkSize], d.values[i])
	}
	return out, nil
}
