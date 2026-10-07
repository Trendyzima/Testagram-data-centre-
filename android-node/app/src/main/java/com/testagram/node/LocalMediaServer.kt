package com.testagram.node

import android.content.Context
import androidx.documentfile.provider.DocumentFile
import java.io.*
import java.net.*
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec

class LocalMediaServer(private val context: Context, private val port: Int = 8788) {
    @Volatile private var running=false
    private var server:ServerSocket?=null
    private val storage=StorageVolumeManager(context)
    private val transcoder=LocalMediaTranscoder(context,storage)
    fun start():Boolean {
        if(running || !storage.hasPersistentAccess() || storage.ensureLayout().isFailure) return false
        return try { server=ServerSocket(port); running=true; Thread({loop()}).start(); true } catch(_:Exception){false}
    }
    fun stop(){running=false;try{server?.close()}catch(_:Exception){};server=null;transcoder.shutdown()}
    private fun loop(){while(running)try{val s=server?.accept();if(s!=null)Thread{handle(s)}.start()}catch(_:Exception){}}
    private fun handle(s:Socket){s.use{socket->val i=BufferedInputStream(socket.getInputStream());val o=BufferedOutputStream(socket.getOutputStream());try{
        val first=line(i)?:return;val p=first.split(' ');if(p.size!=3){reply(o,400,"bad request");return}
        val method=p[0].uppercase();val uri=URI("http://127.0.0.1"+p[1]);val h=mutableMapOf<String,String>()
        while(true){val x=line(i)?:break;if(x.isEmpty())break;val k=x.indexOf(':');if(k>0)h[x.substring(0,k).lowercase()]=x.substring(k+1).trim()}
        if(method=="OPTIONS"){o.write("HTTP/1.1 204 No Content\r\nAccess-Control-Allow-Origin: *\r\nAccess-Control-Allow-Methods: GET,HEAD,PUT,OPTIONS\r\nConnection: close\r\n\r\n".toByteArray());return}
        if(uri.path=="/healthz"){replyJson(o,200,"{\"ok\":true,\"service\":\"local-media\"}");return}
        if(!uri.path.startsWith("/media/")){reply(o,404,"not found");return}
        val key=URLDecoder.decode(uri.path.removePrefix("/media/"),"UTF-8");if(!safe(key)){reply(o,400,"invalid media key");return}
        val token=(h["authorization"]?:"").removePrefix("Bearer ").trim().ifBlank{query(uri,"token")?:""}
        if(!valid(method,key,token)){reply(o,401,"invalid media token");return}
        if(method=="PUT"){upload(i,o,h,key);return}
        if(method=="GET"||method=="HEAD"){download(o,h,key,method=="HEAD");return}
        reply(o,405,"method not allowed")
    }catch(_:Exception){try{reply(o,500,"media server error")}catch(_:Exception){}}}}
    private fun upload(i:InputStream,o:OutputStream,h:Map<String,String>,key:String){val n=h["content-length"]?.toLongOrNull()?:run{reply(o,411,"content-length required");return};if(n<0||n>21474836480L){reply(o,413,"payload too large");return};val f=try{storage.createFile(key,h["content-type"]?.substringBefore(';')?:"application/octet-stream")}catch(_:Exception){reply(o,409,"cannot create media object");return};try{val dst=context.contentResolver.openOutputStream(f.uri,"w")?:error("not writable");dst.use{copy(i,it,n)};replyJson(o,201,"{\"ok\":true}");transcoder.enqueue(key)}catch(_:Exception){f.delete();reply(o,500,"media write failed")}}
    private fun download(o:OutputStream,h:Map<String,String>,key:String,head:Boolean){
        val f=find(key) ?: run { reply(o,404,"not found"); return }
        val d=context.contentResolver.openFileDescriptor(f.uri,"r") ?: run { reply(o,500,"open failed"); return }
        d.use { p ->
            val size=p.statSize
            if(size<0){reply(o,500,"size unavailable");return}
            var start=0L
            var end=size-1
            var partial=false
            val r=h["range"]
            if(r!=null && r.startsWith("bytes=")){
                val spec=r.removePrefix("bytes=").substringBefore(',')
                val dash=spec.indexOf('-')
                if(dash>=0){
                    start=spec.substring(0,dash).toLongOrNull()?:0L
                    end=spec.substring(dash+1).toLongOrNull()?:end
                    if(dash==0){
                        val suffix=spec.substring(1).toLongOrNull()?:0L
                        start=(size-suffix).coerceAtLeast(0)
                    }
                    if(start>=size || end<start){reply(o,416,"range not satisfiable");return}
                    end=end.coerceAtMost(size-1)
                    partial=true
                }
            }
            val len=end-start+1
            val status=if(partial) "206 Partial Content" else "200 OK"
            val rangeHeader=if(partial) "Content-Range: bytes $start-$end/$size\r\n" else ""
            val headers="HTTP/1.1 $status\r\nContent-Type: "+mime(key)+"\r\nContent-Length: $len\r\nAccept-Ranges: bytes\r\n"+rangeHeader+"Access-Control-Allow-Origin: *\r\nConnection: close\r\n\r\n"
            o.write(headers.toByteArray())
            if(!head){
                FileInputStream(p.fileDescriptor).use { src ->
                    src.channel.position(start)
                    val b=ByteArray(65536)
                    var left=len
                    while(left>0){
                        val k=src.read(b,0,minOf(b.size.toLong(),left).toInt())
                        if(k<0)break
                        o.write(b,0,k)
                        left-=k
                    }
                }
            }
            o.flush()
        }
    }
    private fun find(k:String):DocumentFile?{val ps=k.split('/').filter{it.isNotBlank()};if(ps.isEmpty())return null;var c=storage.root()?:return null;for(x in ps.dropLast(1))c=c.findFile(x)?.takeIf{it.isDirectory}?:return null;return c.findFile(ps.last())?.takeIf{it.isFile}}
    private fun valid(m:String,k:String,t:String):Boolean{val p=t.split('.',limit=2);if(p.size!=2)return false;val e=p[1].toLongOrNull()?:return false;if(System.currentTimeMillis()/1000>=e)return false;val s=context.getSharedPreferences("node-config",0).getString("media_signing_secret","")?:"";if(s.length<32)return false;val mac=Mac.getInstance("HmacSHA256");mac.init(SecretKeySpec(s.toByteArray(StandardCharsets.UTF_8),"HmacSHA256"));val x=mac.doFinal((m+"|"+k+"|"+e).toByteArray()).joinToString(""){"%02x".format(it)};return MessageDigest.isEqual(x.toByteArray(),p[0].toByteArray())}
    private fun safe(k:String)=k.isNotBlank()&&!k.startsWith("/")&&!k.contains("..")&&!k.contains('\\')&&k.length<=1024
    private fun query(u:URI,n:String)=u.rawQuery?.split('&')?.firstOrNull{it.startsWith(n+"=")}?.substringAfter('=')
    private fun mime(k:String)=when(k.substringAfterLast('.',"").lowercase()){"jpg","jpeg"->"image/jpeg";"png"->"image/png";"webp"->"image/webp";"mp4"->"video/mp4";"m3u8"->"application/vnd.apple.mpegurl";"ts"->"video/mp2t";else->"application/octet-stream"}
    private fun copy(i:InputStream,o:OutputStream,n:Long){var l=n;val b=ByteArray(65536);while(l>0){val k=i.read(b,0,minOf(b.size.toLong(),l).toInt());if(k<0)error("short upload");o.write(b,0,k);l-=k}}
    private fun line(i:InputStream):String?{val b=ByteArrayOutputStream();while(true){val x=i.read();if(x<0)return null;if(x==10)break;if(x!=13)b.write(x);if(b.size()>8192)error("header too large")};return b.toString("UTF-8")}
    private fun reply(o:OutputStream,s:Int,b:String){o.write(("HTTP/1.1 "+s+" Error\r\nContent-Type: text/plain\r\nContent-Length: "+b.length+"\r\nConnection: close\r\n\r\n"+b).toByteArray());o.flush()}
    private fun replyJson(o:OutputStream,s:Int,b:String){o.write(("HTTP/1.1 "+s+" OK\r\nContent-Type: application/json\r\nContent-Length: "+b.length+"\r\nAccess-Control-Allow-Origin: *\r\nConnection: close\r\n\r\n"+b).toByteArray());o.flush()}
}