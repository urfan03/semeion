package cluster

import (
	"hash/crc32"
	"sort"
	"strconv"
	"sync"
)

type Ring struct {
	mu      sync.RWMutex
	vnodes  int
	keys    []uint32
	owner   map[uint32]string
	members []string
}

func New(members []string, vnodes int) *Ring {
	if vnodes <= 0 {
		vnodes = 128
	}
	r := &Ring{vnodes: vnodes, owner: map[uint32]string{}}
	r.Set(members)
	return r
}

func (r *Ring) Set(members []string) {
	seen := map[string]bool{}
	ms := make([]string, 0, len(members))
	for _, m := range members {
		if m != "" && !seen[m] {
			seen[m] = true
			ms = append(ms, m)
		}
	}
	sort.Strings(ms)
	keys := make([]uint32, 0, len(ms)*r.vnodes)
	owner := make(map[uint32]string, len(ms)*r.vnodes)
	for _, m := range ms {
		for v := 0; v < r.vnodes; v++ {
			h := crc32.ChecksumIEEE([]byte(m + "#" + strconv.Itoa(v)))
			if _, clash := owner[h]; clash {
				continue
			}
			keys = append(keys, h)
			owner[h] = m
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	r.mu.Lock()
	r.keys, r.owner, r.members = keys, owner, ms
	r.mu.Unlock()
}

func (r *Ring) Owner(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.keys) == 0 {
		return ""
	}
	h := crc32.ChecksumIEEE([]byte(key))
	i := sort.Search(len(r.keys), func(i int) bool { return r.keys[i] >= h })
	if i == len(r.keys) {
		i = 0
	}
	return r.owner[r.keys[i]]
}

func (r *Ring) Members() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.members...)
}
