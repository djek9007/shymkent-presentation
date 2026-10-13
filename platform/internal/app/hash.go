package app

import "golang.org/x/crypto/bcrypt"

func passwordHash(s string) (string, error) {
	b, e := bcrypt.GenerateFromPassword([]byte(s), 12)
	return string(b), e
}
