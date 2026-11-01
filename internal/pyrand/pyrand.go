// Package pyrand is Python's random.Random, bit for bit, for a str
// seed: the same Mersenne Twister, seeded the same way, and the same
// arithmetic in each draw, so a seed gives the game the same dice.
package pyrand

import (
	"crypto/sha512"
	"math/big"
	"math/bits"
	"sort"
)

const (
	n         = 624
	m         = 397
	matrixA   = 0x9908b0df
	upperMask = 0x80000000
	lowerMask = 0x7fffffff
)

// Rand is random.Random(seed) for a str seed (version 2 seeding:
// the bytes and their SHA-512, as one big integer).
type Rand struct {
	mt  [n]uint32
	idx int
}

// New is random.Random(seed).
func New(seed string) *Rand {
	b := []byte(seed)
	h := sha512.Sum512(b)
	num := new(big.Int).SetBytes(append(b, h[:]...))
	var key []uint32
	for _, w := range num.Bits() { // least significant first
		if bits.UintSize == 64 {
			key = append(key, uint32(w), uint32(uint64(w)>>32))
		} else {
			key = append(key, uint32(w))
		}
	}
	for len(key) > 1 && key[len(key)-1] == 0 {
		key = key[:len(key)-1]
	}
	if len(key) == 0 {
		key = []uint32{0}
	}
	r := &Rand{}
	r.initByArray(key)
	return r
}

func (r *Rand) initGenrand(s uint32) {
	r.mt[0] = s
	for i := 1; i < n; i++ {
		r.mt[i] = 1812433253*(r.mt[i-1]^(r.mt[i-1]>>30)) + uint32(i)
	}
	r.idx = n
}

func (r *Rand) initByArray(key []uint32) {
	r.initGenrand(19650218)
	i, j := 1, 0
	k := n
	if len(key) > k {
		k = len(key)
	}
	for ; k > 0; k-- {
		r.mt[i] = (r.mt[i] ^ ((r.mt[i-1] ^ (r.mt[i-1] >> 30)) *
			1664525)) + key[j] + uint32(j)
		i++
		j++
		if i >= n {
			r.mt[0] = r.mt[n-1]
			i = 1
		}
		if j >= len(key) {
			j = 0
		}
	}
	for k = n - 1; k > 0; k-- {
		r.mt[i] = (r.mt[i] ^ ((r.mt[i-1] ^ (r.mt[i-1] >> 30)) *
			1566083941)) - uint32(i)
		i++
		if i >= n {
			r.mt[0] = r.mt[n-1]
			i = 1
		}
	}
	r.mt[0] = 0x80000000
}

// uint32 is genrand_uint32.
func (r *Rand) uint32() uint32 {
	if r.idx >= n {
		var kk int
		for kk = 0; kk < n-m; kk++ {
			y := r.mt[kk]&upperMask | r.mt[kk+1]&lowerMask
			r.mt[kk] = r.mt[kk+m] ^ (y >> 1) ^ (y&1)*matrixA
		}
		for ; kk < n-1; kk++ {
			y := r.mt[kk]&upperMask | r.mt[kk+1]&lowerMask
			r.mt[kk] = r.mt[kk+(m-n)] ^ (y >> 1) ^ (y&1)*matrixA
		}
		y := r.mt[n-1]&upperMask | r.mt[0]&lowerMask
		r.mt[n-1] = r.mt[m-1] ^ (y >> 1) ^ (y&1)*matrixA
		r.idx = 0
	}
	y := r.mt[r.idx]
	r.idx++
	y ^= y >> 11
	y ^= (y << 7) & 0x9d2c5680
	y ^= (y << 15) & 0xefc60000
	y ^= y >> 18
	return y
}

// Random is random(): 53 random bits in [0, 1).
func (r *Rand) Random() float64 {
	a, b := r.uint32()>>5, r.uint32()>>6
	return (float64(a)*67108864.0 + float64(b)) *
		(1.0 / 9007199254740992.0)
}

// GetRandBits is getrandbits(k), k <= 64: 32-bit words, least
// significant first, the last one cut to what is left.
func (r *Rand) GetRandBits(k int) uint64 {
	if k <= 0 {
		return 0
	}
	if k > 64 {
		k = 64
	}
	if k <= 32 {
		return uint64(r.uint32() >> (32 - k))
	}
	lo := uint64(r.uint32())
	hi := uint64(r.uint32() >> (64 - k))
	return hi<<32 | lo
}

// RandBelow is _randbelow(n): getrandbits(n.bit_length()) until it
// is below n. n < 1 gives 0 (Python never asks for that).
func (r *Rand) RandBelow(n int) int {
	if n < 1 {
		return 0
	}
	k := bits.Len64(uint64(n))
	v := r.GetRandBits(k)
	for v >= uint64(n) {
		v = r.GetRandBits(k)
	}
	return int(v)
}

// RandInt is randint(a, b), b included.
func (r *Rand) RandInt(a, b int) int {
	return a + r.RandBelow(b-a+1)
}

// Uniform is uniform(a, b): a + (b-a) * random().
func (r *Rand) Uniform(a, b float64) float64 {
	return a + (b-a)*r.Random()
}

// Choice is choice(seq) as an index into a sequence of n items.
func (r *Rand) Choice(n int) int { return r.RandBelow(n) }

// Choices is choices(population, weights)[0] as an index: the running
// sums of weights, random() times their total, bisect_right.
func (r *Rand) Choices(weights []float64) int {
	if len(weights) == 0 {
		return 0
	}
	cum := make([]float64, len(weights))
	t := 0.0
	for i, w := range weights {
		t += w
		cum[i] = t
	}
	x := r.Random() * (t + 0.0)
	hi := len(cum) - 1
	return sort.Search(hi, func(i int) bool { return cum[i] > x })
}
