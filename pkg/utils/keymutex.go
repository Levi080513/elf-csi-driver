// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"sync"
)

// KeyMutex is a thread-safe interface for acquiring locks on arbitrary strings.
type KeyMutex interface {
	// Acquires a lock associated with the specified ID, creates the lock if one doesn't already exist.
	LockKey(id string)

	TryLockKey(id string) bool
	// Releases the lock associated with the specified ID.
	// Returns an error if the specified ID doesn't exist.
	UnlockKey(id string) error
}

// Returns a new instance of a key mutex.
func NewKeyMutex() KeyMutex {
	return &keyMutex{
		mutexMap: make(map[string]*sync.Mutex),
	}
}

type keyMutex struct {
	sync.RWMutex
	mutexMap map[string]*sync.Mutex
}

// Acquires a lock associated with the specified ID (creates the lock if one doesn't already exist).
func (km *keyMutex) LockKey(id string) {
	mutex := km.getOrCreateLock(id)
	mutex.Lock()
}

// Acquires a lock associated with the specified ID (creates the lock if one doesn't already exist).
func (km *keyMutex) TryLockKey(id string) bool {
	mutex := km.getOrCreateLock(id)
	return mutex.TryLock()
}

// Releases the lock associated with the specified ID.
// Returns an error if the specified ID doesn't exist.
func (km *keyMutex) UnlockKey(id string) error {
	km.RLock()
	defer km.RUnlock()
	mutex, exists := km.mutexMap[id]

	if !exists {
		return fmt.Errorf("id %q not found", id)
	}

	mutex.Unlock()

	return nil
}

// Returns lock associated with the specified ID, or creates the lock if one doesn't already exist.
func (km *keyMutex) getOrCreateLock(id string) *sync.Mutex {
	km.Lock()
	defer km.Unlock()

	if _, exists := km.mutexMap[id]; !exists {
		km.mutexMap[id] = &sync.Mutex{}
	}

	return km.mutexMap[id]
}
