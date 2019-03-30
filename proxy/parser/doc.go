// Package parser transter conn to be a parser conn
//
// init request payload:
//   <type>(1) + <size>(2))(+Overhead) + <data>(size+Overhead)
// data definition:
//   0x00(any):   [size](1) + [addr:port] + content
//   0x01(http):  content
//   0x02(https): [port](2) + content
//
// init response payload:
//   ([status code](2) + <size>(2))(+Overhead) + <content>(size+Overhead)
package parser
