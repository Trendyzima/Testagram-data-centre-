package node
import("os";"path/filepath";"testing")
func TestVolumeCannotEscapeRoot(t *testing.T){if _,err:=cleanVolume(t.TempDir(),"../escape");err==nil{t.Fatal("expected traversal rejection")}}
func TestVolumePath(t *testing.T){root:=t.TempDir();n:=New("test",root);p,err:=cleanVolume(n.DataRoot,"abc");if err!=nil{t.Fatal(err)};if p!=filepath.Join(root,"volumes","abc"){t.Fatal(p)};if err=os.MkdirAll(p,0700);err!=nil{t.Fatal(err)}}
