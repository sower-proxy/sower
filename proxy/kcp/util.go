package kcp

func fillPassword(password string) []byte {
	for len(password) < 16 {
		password += password
	}
	return []byte(password)[:16]
}
