// transter conn to be a crypto conn
// stream mode, blocksize is 1350, for mtu
// data  [0   ...   1332]
// block [size][2 ... 1350]
// payload size + data size + aead overhead => (2+1332+16)
package shadow
