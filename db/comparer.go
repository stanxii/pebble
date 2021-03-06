// Copyright 2011 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package db

import (
	"bytes"
	"encoding/binary"
)

// TODO(tbg): introduce a FeasibleKey type to make things clearer.

// Compare returns -1, 0, or +1 depending on whether a is 'less than',
// 'equal to' or 'greater than' b. The two arguments can only be 'equal'
// if their contents are exactly equal. Furthermore, the empty slice
// must be 'less than' any non-empty slice.
//
// TODO(tbg): clarify what keys this needs to compare. It definitely needs to
// compare feasible keys (i.e. any user keys, but also those returned by
// Successor and Separator), as well as keys returned by Split (which are not
// themselves feasible, but are feasible keys with their version suffix
// trimmmed). Anything else?
type Compare func(a, b []byte) int

// Equal returns true if a and b are equivalent. For a given Compare,
// Equal(a,b) must return true iff Compare(a,b) returns zero, that is,
// Equal is a (potentially faster) specialization of Compare.
type Equal func(a, b []byte) bool

// AbbreviatedKey returns a fixed length prefix of a user key such that AbbreviatedKey(a)
// < AbbreviatedKey(b) iff a < b and AbbreviatedKey(a) > AbbreviatedKey(b) iff a > b. If
// AbbreviatedKey(a) == AbbreviatedKey(b) an additional comparison is required to
// determine if the two keys are actually equal.
//
// This helps optimize indexed batch comparisons for cache locality. If a Split
// function is specified, AbbreviatedKey usually returns the first eight bytes
// of the user key prefix in the order that gives the correct ordering.
type AbbreviatedKey func(key []byte) uint64

// Given feasible keys a, b for which Compare(a, b) < 0, Separator returns a
// feasible key k such that:
//
// 1. Compare(a, k) <= 0, and
// 2. Compare(k, b) < 0.
//
// As a special case, b may be nil in which case the second condition is dropped.
//
// Separator is used to construct SSTable index blocks. A trivial implementation
// is `return a`, but appending fewer bytes leads to smaller SSTables.
//
// For example, if dst, a and b are the []byte equivalents of the strings
// "aqua", "black" and "blue", then the result may be "aquablb".
// Similarly, if the arguments were "aqua", "green" and "", then the result
// may be "aquah".
type Separator func(dst, a, b []byte) []byte

// Given a feasible key a, Successor returns feasible key k such that Compare(k,
// a) >= 0. A simple implementation may return a unchanged. The dst parameter
// may be used to store the returned key, though it is valid to pass a nil. The
// returned key must be feasible.
//
// TODO(tbg) it seems that Successor is just the special case of Separator in
// which b is nil. Can we remove this?
type Successor func(dst, a []byte) []byte

// Split returns the length of the prefix of the user key that corresponds to
// the key portion of an MVCC encoding scheme to enable the use of prefix bloom
// filters.
//
// The method will only ever be called with feasible keys, that is, keys that
// the user could potentially store in the database. Typically this means
// that the method must only handle valid MVCC encoded keys and should panic
// on any other input.
//
// A trivial MVCC scheme is one in which Split() returns len(a). This
// corresponds to assigning a constant version to each key in the database. For
// performance reasons, it is preferable to use a `nil` split in this case.
//
// The returned prefix must have the following properties (where a and b are
// feasible):
//
// 1) Compare(prefix(a), a) <= 0,
// 2) If Compare(a, b) <= 0, then Compare(prefix(a), prefix(b)) <= 0
// 3) if b begins with a, then prefix(b) = prefix(a).
type Split func(a []byte) int

// Comparer defines a total ordering over the space of []byte keys: a 'less
// than' relationship.
type Comparer struct {
	Compare        Compare
	Equal          Equal
	AbbreviatedKey AbbreviatedKey
	Separator      Separator
	Split          Split
	Successor      Successor

	// Name is the name of the comparer.
	//
	// The Level-DB on-disk format stores the comparer name, and opening a
	// database with a different comparer from the one it was created with
	// will result in an error.
	Name string
}

// DefaultComparer is the default implementation of the Comparer interface.
// It uses the natural ordering, consistent with bytes.Compare.
var DefaultComparer = &Comparer{
	Compare: bytes.Compare,
	Equal:   bytes.Equal,

	AbbreviatedKey: func(key []byte) uint64 {
		if len(key) >= 8 {
			return binary.BigEndian.Uint64(key)
		}
		var v uint64
		for _, b := range key {
			v <<= 8
			v |= uint64(b)
		}
		return v << uint(8*(8-len(key)))
	},

	Separator: func(dst, a, b []byte) []byte {
		i, n := SharedPrefixLen(a, b), len(dst)
		dst = append(dst, a...)

		min := len(a)
		if min > len(b) {
			min = len(b)
		}
		if i >= min {
			// Do not shorten if one string is a prefix of the other.
			return dst
		}

		if a[i] >= b[i] {
			// b is smaller than a or a is already the shortest possible.
			return dst
		}

		if i < len(b)-1 || a[i]+1 < b[i] {
			i += n
			dst[i]++
			return dst[:i+1]
		}

		i += n + 1
		for ; i < len(dst); i++ {
			if dst[i] != 0xff {
				dst[i]++
				return dst[:i+1]
			}
		}
		return dst
	},

	Successor: func(dst, a []byte) []byte {
		for i := 0; i < len(a); i++ {
			if a[i] != 0xff {
				dst = append(dst, a[:i+1]...)
				dst[len(dst)-1]++
				return dst
			}
		}
		return append(dst, a...)
	},

	// This name is part of the C++ Level-DB implementation's default file
	// format, and should not be changed.
	Name: "leveldb.BytewiseComparator",
}

// SharedPrefixLen returns the largest i such that a[:i] equals b[:i].
// This function can be useful in implementing the Comparer interface.
func SharedPrefixLen(a, b []byte) int {
	i, n := 0, len(a)
	if n > len(b) {
		n = len(b)
	}
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
