import socket,struct,sys
name=sys.argv[1].encode()
m=struct.pack(">HHHHHH",1,0x100,1,0,0,0)
for p in name.split(b"."): m+=bytes([len(p)])+p
m+=b"\x00"+struct.pack(">HH",1,1)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.settimeout(8)
try:
    s.sendto(m,("10.8.0.1",53)); d,_=s.recvfrom(2048)
    anc=struct.unpack(">H",d[6:8])[0]
    print(".".join(str(b) for b in d[-4:]) if anc else "none")
except Exception as e:
    print("FAILED",e)
