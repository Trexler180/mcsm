package auth

import "golang.org/x/crypto/bcrypt"

// dummyHash is a valid bcrypt hash of a random string at DefaultCost. DummyCheck
// runs a comparison against it so a login for a non-existent user spends the
// same CPU as one for a real user, closing the timing side-channel that would
// otherwise let an attacker enumerate valid accounts.
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// DummyCheck performs a throwaway bcrypt comparison to equalize the timing of a
// failed lookup with a real password check. The result is meaningless and must
// be ignored.
func DummyCheck(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
}
