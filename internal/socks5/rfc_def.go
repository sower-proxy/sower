package socks5

// https://tools.ietf.org/html/rfc1928

type authReq struct {
	VER      byte
	NMETHODS byte
	METHODS  [1]byte // 1 to 255, fix to no authentication
}

type authResp struct {
	VER    byte
	METHOD byte
}

type request struct {
	req
	DST_ADDR []byte // first byte is length
	DST_PORT []byte // two bytes
}
type req struct {
	VER  byte
	CMD  byte
	RSV  byte
	ATYP byte
}

func (r *request) Bytes() []byte {
	out := []byte{r.VER, r.CMD, r.RSV, r.ATYP}
	out = append(out, r.DST_ADDR...)
	return append(out, r.DST_PORT...)
}

type response struct {
	resp
	DST_ADDR []byte // first byte is length
	DST_PORT []byte // two bytes
}
type resp struct {
	VER  byte
	REP  byte
	RSV  byte
	ATYP byte
}
