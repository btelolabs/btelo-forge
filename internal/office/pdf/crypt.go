package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
)

// The standard security handler, versions 1 to 5. The key comes from the user password; a document that opens with
// the empty password does so silently. Edits saved into an encrypted file are encrypted the same way.

type cryptMethod int

const (
	cryptNone cryptMethod = iota
	cryptRC4
	cryptAESV2
	cryptAESV3
)

type crypt struct {
	key             []byte
	stm, str        cryptMethod
	encryptMetadata bool
	dictNum         int // the Encrypt dictionary's object, whose strings are not encrypted
}

var padding = []byte{0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A}

func (d *Doc) isEncryptDict(num int) bool { return d.crypt != nil && d.crypt.dictNum == num }

// Encrypted reports whether the document is encrypted.
func (d *Doc) Encrypted() bool { return d.crypt != nil }

func (d *Doc) initCrypt(password string) error {
	enc := d.trailer["Encrypt"]
	if enc == nil {
		return nil
	}
	d.crypt = nil
	dictNum := 0
	if r, ok := enc.(Ref); ok {
		dictNum = r.Num
	}
	dict := d.Dict(enc)
	if dict == nil {
		return errors.New("pdf: the encryption dictionary is missing")
	}
	if f := d.Name(dict["Filter"]); f != "Standard" {
		return errors.New("pdf: encrypted with an unsupported security handler: " + string(f))
	}
	v, r := d.Int(dict["V"], 0), d.Int(dict["R"], 2)
	length := d.Int(dict["Length"], 40)
	if length < 40 {
		length *= 8 // some writers give bytes
	}
	c := &crypt{stm: cryptRC4, str: cryptRC4, encryptMetadata: d.Bool(dict["EncryptMetadata"], true), dictNum: dictNum}
	if v >= 4 {
		cf := d.Dict(dict["CF"])
		method := func(key Name) cryptMethod {
			name := d.Name(dict[key])
			if name == "" || name == "Identity" {
				return cryptNone
			}
			f := d.Dict(cf[name])
			if l := d.Int(f["Length"], 0); l > 0 {
				if l <= 32 {
					l *= 8
				}
				length = l
			}
			switch d.Name(f["CFM"]) {
			case "V2":
				return cryptRC4
			case "AESV2":
				length = 128
				return cryptAESV2
			case "AESV3":
				length = 256
				return cryptAESV3
			}
			return cryptNone
		}
		c.stm, c.str = method("StmF"), method("StrF")
	} else if v == 1 {
		length = 40
	}
	var fileID []byte
	if ids := d.Array(d.trailer["ID"]); len(ids) > 0 {
		if s, ok := d.Resolve(ids[0]).(String); ok {
			fileID = []byte(s)
		}
	}
	str := func(k Name) []byte { s, _ := d.Resolve(dict[k]).(String); return []byte(s) }
	o, u, oe, ue := str("O"), str("U"), str("OE"), str("UE")
	p := int32(d.Int(dict["P"], 0))
	if r >= 5 {
		key, ok := keyV5([]byte(password), o, u, oe, ue, r)
		if !ok {
			return ErrPassword
		}
		c.key = key
	} else {
		n := length / 8
		if r == 2 {
			n = 5
		}
		key := keyLegacy([]byte(password), o, p, fileID, n, r, c.encryptMetadata)
		if !checkUser(key, u, fileID, r) {
			// The password may be the owner's.
			upw := ownerToUser([]byte(password), o, n, r)
			key = keyLegacy(upw, o, p, fileID, n, r, c.encryptMetadata)
			if !checkUser(key, u, fileID, r) {
				return ErrPassword
			}
		}
		c.key = key
	}
	d.crypt = c
	// Objects read before the key was known hold undecrypted strings.
	d.mu.Lock()
	d.cache = map[int]Object{}
	d.objStms = map[int]*objStm{}
	d.mu.Unlock()
	if d.repaired {
		var streams []int
		for _, n := range d.objectNumbers() {
			if s, ok := d.Object(n).(*Stream); ok && s.Dict["Type"] == Name("ObjStm") {
				streams = append(streams, n)
			}
		}
		d.packedAfterScan(streams)
	}
	return nil
}

func pad(pw []byte) []byte { return append(bytes.Clone(pw[:min(len(pw), 32)]), padding...)[:32] }

func keyLegacy(pw, o []byte, p int32, id []byte, n, r int, meta bool) []byte {
	h := md5.New()
	h.Write(pad(pw))
	h.Write(o[:min(len(o), 32)])
	var pb [4]byte
	binary.LittleEndian.PutUint32(pb[:], uint32(p))
	h.Write(pb[:])
	h.Write(id)
	if r >= 4 && !meta {
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	sum := h.Sum(nil)
	n = min(max(n, 5), 16)
	if r >= 3 {
		for range 50 {
			s := md5.Sum(sum[:n])
			sum = s[:]
		}
	}
	return sum[:n]
}

func rc4Bytes(key, in []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return in
	}
	out := make([]byte, len(in))
	c.XORKeyStream(out, in)
	return out
}

func checkUser(key, u, id []byte, r int) bool {
	if r == 2 {
		return bytes.Equal(rc4Bytes(key, padding), u[:min(len(u), 32)])
	}
	h := md5.New()
	h.Write(padding)
	h.Write(id)
	x := rc4Bytes(key, h.Sum(nil))
	for i := 1; i <= 19; i++ {
		k := bytes.Clone(key)
		for j := range k {
			k[j] ^= byte(i)
		}
		x = rc4Bytes(k, x)
	}
	return len(u) >= 16 && bytes.Equal(x, u[:16])
}

func ownerToUser(pw, o []byte, n, r int) []byte {
	sum := md5.Sum(pad(pw))
	key := sum[:]
	if r >= 3 {
		for range 50 {
			s := md5.Sum(key)
			key = s[:]
		}
	}
	key = key[:min(max(n, 5), 16)]
	x := bytes.Clone(o[:min(len(o), 32)])
	if r == 2 {
		return rc4Bytes(key, x)
	}
	for i := 19; i >= 0; i-- {
		k := bytes.Clone(key)
		for j := range k {
			k[j] ^= byte(i)
		}
		x = rc4Bytes(k, x)
	}
	return x
}

// keyV5 is algorithm 2.A: the file key, from the user or the owner password.
func keyV5(pw, o, u, oe, ue []byte, r int) ([]byte, bool) {
	if len(pw) > 127 {
		pw = pw[:127]
	}
	if len(o) < 48 || len(u) < 48 || len(oe) < 32 || len(ue) < 32 {
		return nil, false
	}
	hash := func(salt, extra []byte) []byte {
		if r == 5 {
			h := sha256.New()
			h.Write(pw)
			h.Write(salt)
			h.Write(extra)
			return h.Sum(nil)
		}
		return hash2B(pw, salt, extra)
	}
	unwrap := func(k, e []byte) []byte {
		b, err := aes.NewCipher(k)
		if err != nil {
			return nil
		}
		out := make([]byte, 32)
		cipher.NewCBCDecrypter(b, make([]byte, 16)).CryptBlocks(out, e[:32])
		return out
	}
	if bytes.Equal(hash(o[32:40], u[:48]), o[:32]) {
		return unwrap(hash(o[40:48], u[:48]), oe), true
	}
	if bytes.Equal(hash(u[32:40], nil), u[:32]) {
		return unwrap(hash(u[40:48], nil), ue), true
	}
	return nil, false
}

// hash2B is algorithm 2.B of ISO 32000-2.
func hash2B(pw, salt, u []byte) []byte {
	h := sha256.New()
	h.Write(pw)
	h.Write(salt)
	h.Write(u)
	k := h.Sum(nil)
	for round := 0; ; round++ {
		k1 := bytes.Repeat(append(append(bytes.Clone(pw), k...), u...), 64)
		b, err := aes.NewCipher(k[:16])
		if err != nil {
			return k[:32]
		}
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(b, k[16:32]).CryptBlocks(e, k1)
		mod := 0
		for _, c := range e[:16] {
			mod += int(c)
		}
		switch mod % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		default:
			s := sha512.Sum512(e)
			k = s[:]
		}
		if round >= 63 && int(e[len(e)-1]) <= round-31 {
			break
		}
	}
	return k[:32]
}

func (c *crypt) method(stream bool) cryptMethod {
	if stream {
		return c.stm
	}
	return c.str
}

// encrypted reports whether a stream with this dictionary is encrypted at all.
func (c *crypt) encrypted(dict Dict) bool {
	if dict["Type"] == Name("XRef") {
		return false
	}
	if dict["Type"] == Name("Metadata") && !c.encryptMetadata {
		return false
	}
	if a, ok := dict["Filter"].(Array); ok {
		for _, f := range a {
			if f == Name("Crypt") {
				return false // only the Identity crypt filter is common; a named one would need its own method
			}
		}
	}
	return dict["Filter"] != Name("Crypt")
}

func (c *crypt) objectKey(num, gen int, m cryptMethod) []byte {
	if m == cryptAESV3 {
		return c.key
	}
	h := md5.New()
	h.Write(c.key)
	h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), byte(gen), byte(gen >> 8)})
	if m == cryptAESV2 {
		h.Write([]byte("sAlT"))
	}
	return h.Sum(nil)[:min(len(c.key)+5, 16)]
}

func (c *crypt) decrypt(num, gen int, in []byte, stream bool) []byte {
	m := c.method(stream)
	switch m {
	case cryptNone:
		return in
	case cryptRC4:
		return rc4Bytes(c.objectKey(num, gen, m), in)
	}
	if len(in) < 32 || (len(in)-16)%16 != 0 {
		// A damaged length: decrypt the whole blocks there are.
		if len(in) < 32 {
			return nil
		}
		in = in[:16+(len(in)-16)/16*16]
	}
	b, err := aes.NewCipher(c.objectKey(num, gen, m))
	if err != nil {
		return in
	}
	out := make([]byte, len(in)-16)
	cipher.NewCBCDecrypter(b, in[:16]).CryptBlocks(out, in[16:])
	if n := int(out[len(out)-1]); n >= 1 && n <= 16 && n <= len(out) {
		out = out[:len(out)-n]
	}
	return out
}

func (c *crypt) encrypt(num, gen int, in []byte, stream bool) []byte {
	m := c.method(stream)
	switch m {
	case cryptNone:
		return in
	case cryptRC4:
		return rc4Bytes(c.objectKey(num, gen, m), in)
	}
	b, err := aes.NewCipher(c.objectKey(num, gen, m))
	if err != nil {
		return in
	}
	n := 16 - len(in)%16
	plain := append(bytes.Clone(in), bytes.Repeat([]byte{byte(n)}, n)...)
	out := make([]byte, 16+len(plain))
	if _, err := rand.Read(out[:16]); err != nil {
		return in
	}
	cipher.NewCBCEncrypter(b, out[:16]).CryptBlocks(out[16:], plain)
	return out
}
