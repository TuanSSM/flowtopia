package main

import "sync"

// The tests start a server for nearly every test, and each start makes the admin account with the password the tests
// use (flowa-test-only, never the real one): its hash, PBKDF2 at the full count of rounds, took about a third of a second
// under the race detector each time, a minute over the database tests. That one password's hash is made once and kept
// for every start in this process; every other password is hashed as always, so the tests of passwords, sign-in and
// their limits see real hashes with a salt of their own. The kept hash is a real hash of that password, which signs in
// as before.
func init() {
	real := hashPassword
	var once sync.Once
	var kept string
	hashPassword = func(pw string) string {
		if pw != "flowa-test-only" {
			return real(pw)
		}
		once.Do(func() { kept = real(pw) })
		return kept
	}
}
