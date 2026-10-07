package node

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Node struct { ID string; DataRoot string }

type Info struct {
	ID string `json:"id"`; Name string `json:"name"`; OS string `json:"os"`; Arch string `json:"arch"`
	CPUCores int `json:"cpuCores"`; MemoryBytes uint64 `json:"memoryBytes"`; StorageBytes uint64 `json:"storageBytes"`
	FreeBytes uint64 `json:"freeBytes"`; LastSeen time.Time `json:"lastSeen"`; Status string `json:"status"`
}

func New(id, root string) *Node { return &Node{ID:id, DataRoot:root} }
func (n *Node) Info() Info { return Info{ID:n.ID,Name:n.ID,OS:runtime.GOOS,Arch:runtime.GOARCH,CPUCores:runtime.NumCPU(),LastSeen:time.Now().UTC(),Status:"online"} }

func cleanVolume(root,id string)(string,error){
	if id==""||id=="."||id==".."||strings.ContainsAny(id,"/\\"){return "",errors.New("invalid volume id")}
	p:=filepath.Join(root,"volumes",id); rel,err:=filepath.Rel(root,p)
	if err!=nil||rel==".."||strings.HasPrefix(rel,".."+string(filepath.Separator)){return "",errors.New("volume escapes data root")}
	return p,nil
}
func inside(root,p string)bool{rel,err:=filepath.Rel(root,p);return err==nil&&rel!=".."&&!strings.HasPrefix(rel,".."+string(filepath.Separator))}

func(n *Node)VolumeHandler(w http.ResponseWriter,r *http.Request){
	parts:=strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path,"/v1/volumes/"),"/"),"/")
	if len(parts)<1||parts[0]==""{http.Error(w,"missing volume",400);return}
	dir,err:=cleanVolume(n.DataRoot,parts[0]);if err!=nil{http.Error(w,err.Error(),400);return}
	if err=os.MkdirAll(dir,0700);err!=nil{http.Error(w,"cannot create volume",500);return}
	switch r.Method{
	case http.MethodGet:
		if len(parts)==1{writeJSON(w,200,map[string]any{"id":parts[0],"path":dir});return}
		file:=filepath.Join(append([]string{dir},parts[1:]...)...);if !inside(dir,file){http.Error(w,"path escapes volume",400);return}
		data,err:=os.ReadFile(file);if err!=nil{http.Error(w,"not found",404);return};w.Header().Set("Content-Type","application/octet-stream");_,_=w.Write(data)
	case http.MethodPut:
		if len(parts)<2{http.Error(w,"file path required",400);return};file:=filepath.Join(append([]string{dir},parts[1:]...)...)
		if !inside(dir,file){http.Error(w,"path escapes volume",400);return};if err=os.MkdirAll(filepath.Dir(file),0700);err!=nil{http.Error(w,"cannot create parent",500);return}
		f,err:=os.OpenFile(file,os.O_CREATE|os.O_WRONLY|os.O_TRUNC,0600);if err!=nil{http.Error(w,"cannot open file",500);return};defer f.Close()
		if _,err=io.Copy(f,io.LimitReader(r.Body,1<<30));err!=nil{http.Error(w,"write failed",500);return};writeJSON(w,201,map[string]any{"ok":true})
	default:w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func writeJSON(w http.ResponseWriter,code int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(code);_=json.NewEncoder(w).Encode(v)}
