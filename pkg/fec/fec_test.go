package fec

import (
	"bytes"
	"crypto/rand"
	mrand "math/rand/v2"
	"testing"
)

func TestGF256FieldAxioms(t *testing.T) {
	seen := make(map[uint8]bool)
	for i := 0; i < 255; i++ {
		v := expTable[i]
		if v == 0 {
			t.Fatalf("expTable[%d] == 0", i)
		}
		seen[v] = true
	}
	if len(seen) != 255 {
		t.Fatalf("generator 2 only generated %d elements, want 255", len(seen))
	}
	for a := 1; a < 256; a++ {
		inv := gfInv(uint8(a))
		if got := gfMul(uint8(a), inv); got != 1 {
			t.Fatalf("a=%d * inv=%d = %d, want 1", a, inv, got)
		}
	}
}

func TestCauchyMDSAnySubsetReconstructs(t *testing.T) {
	rng := mrand.New(mrand.NewPCG(42, 99))
	for _, k := range []int{1, 2, 5, 16, 37, MaxK} {
		chunkSize := 16
		orig := make([]byte, k*chunkSize-3)
		if len(orig) == 0 {
			orig = []byte{0xAB}
		} else {
			_, _ = rand.Read(orig)
		}

		enc, err := NewEncoder(orig, chunkSize)
		if err != nil {
			t.Fatal(err)
		}
		if enc.K() != k {
			t.Fatalf("got K=%d want %d", enc.K(), k)
		}

		// Precompute all 256 encoded chunks (systematic + Cauchy parity).
		all := make([][]byte, 256)
		for seq := 0; seq < 256; seq++ {
			all[seq] = enc.Encode(uint8(seq))
		}

		// Test 1: Pure parity chunks only (100% systematic packet loss!).
		decParity, err := NewDecoder(k, chunkSize)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < k; i++ {
			seq := uint8(k + i*2)
			done, useful, err := decParity.Add(seq, all[seq])
			if err != nil || !useful {
				t.Fatalf("k=%d i=%d seq=%d useful=%v err=%v", k, i, seq, useful, err)
			}
			if (i == k-1) != done {
				t.Fatalf("k=%d i=%d unexpected done=%v", k, i, done)
			}
		}
		got, err := decParity.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got[:len(orig)], orig) {
			t.Fatalf("k=%d pure-parity reconstruction mismatch", k)
		}

		// Test 2: 20 random subsets of K distinct seqs out of 0..255 + duplicates.
		for trial := 0; trial < 20; trial++ {
			perm := rng.Perm(256)
			dec, _ := NewDecoder(k, chunkSize)
			for i := 0; i < k; i++ {
				seq := uint8(perm[i])
				done, useful, err := dec.Add(seq, all[seq])
				if err != nil || !useful {
					t.Fatalf("k=%d trial=%d i=%d seq=%d not useful!", k, trial, i, seq)
				}
				// Re-adding duplicate must be ignored cleanly.
				_, dupUseful, _ := dec.Add(seq, all[seq])
				if dupUseful {
					t.Fatal("duplicate seq reported useful")
				}
				if i == k-1 && !done {
					t.Fatal("expected completion after K distinct chunks")
				}
			}
			rec, err := dec.Bytes()
			if err != nil || !bytes.Equal(rec[:len(orig)], orig) {
				t.Fatalf("k=%d trial=%d mismatch", k, trial)
			}
		}
	}
}

func TestFECEdgeCasesAndValidation(t *testing.T) {
	if _, err := NewEncoder([]byte("abc"), 0); err != ErrInvalidParams {
		t.Fatalf("expected ErrInvalidParams for chunkSize=0, got %v", err)
	}
	if _, err := NewEncoder(make([]byte, (MaxK+1)*4), 4); err == nil {
		t.Fatal("expected K > MaxK to fail")
	}
	emptyEnc, err := NewEncoder(nil, 8)
	if err != nil || emptyEnc.K() != 1 || emptyEnc.ChunkSize() != 8 {
		t.Fatalf("unexpected empty encoder state: k=%d size=%d err=%v", emptyEnc.K(), emptyEnc.ChunkSize(), err)
	}

	if _, err := NewDecoder(0, 8); err != ErrInvalidParams {
		t.Fatal("expected k=0 decoder error")
	}
	if _, err := NewDecoder(MaxK+1, 8); err != ErrInvalidParams {
		t.Fatal("expected k>MaxK decoder error")
	}

	dec, err := NewDecoder(3, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dec.Add(0, []byte("short")); err != ErrChunkLength {
		t.Fatalf("expected ErrChunkLength, got %v", err)
	}
	if _, err := dec.Bytes(); err == nil {
		t.Fatal("Bytes() on incomplete decoder must return error")
	}

	src := bytes.Repeat([]byte{0x42}, 20)
	enc, _ := NewEncoder(src, 8)
	_, _, _ = dec.Add(0, enc.Encode(0))
	_, _, _ = dec.Add(10, enc.Encode(10)) // parity chunk
	if dec.Rank() != 2 || dec.Needed() != 1 || dec.RepairsUsed() != 1 || dec.Complete() {
		t.Fatalf("unexpected partial decoder stats: rank=%d need=%d rep=%d", dec.Rank(), dec.Needed(), dec.RepairsUsed())
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected gfInv(0) to panic")
		}
	}()
	_ = gfInv(0)
}
