package main
import("os";"syscall";"encoding/json")
func main(){
 f,e:=os.Open("/dev/mem");if e!=nil{panic(e)};defer f.Close()
 b,e:=syscall.Mmap(int(f.Fd()),0x8037a000,0x5000,syscall.PROT_READ,syscall.MAP_SHARED);if e!=nil{panic(e)};defer syscall.Munmap(b)
 if e=json.NewEncoder(os.Stdout).Encode(b);e!=nil{panic(e)}
}
