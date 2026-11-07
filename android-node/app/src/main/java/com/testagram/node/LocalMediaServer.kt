package com.testagram.node

import android.content.Context
import androidx.documentfile.provider.DocumentFile
import java.io.*
import java.net.*
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ThreadFactory
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit

class LocalMediaServer(private val context: Context, private val port: Int = 8788) {
    @Volatile private var running=false
    private var server:ServerSocket?=null
    private val storage=StorageVolumeManager(context)
    // Keep phone-side concurrency bounded: never spawn an unbounded thread per request.
    private val workers = ThreadPoolExecutor(2, 2, 30, TimeUnit.SECONDS, ArrayBlockingQueue(4),
        ThreadFactory { task -> Thread(task, "testagram-media-io").apply { isDaemon = true } },
        ThreadPoolExecutor.AbortPolicy())
    fun start():Boolean {
        if(running || !storage.hasPersistentAccess() || storage.ensureLayout().isFailure) return false
        return try {
            server = ServerSocket().apply {
                reuseAddress = false
                bind(InetSocketAddress(InetAddress.getByName("127.0.0.1"), port), 16)
            }
            running = true
            Thread({ loop() }, "testagram-media-accept").apply { isDaemon = true }.start()
            true
        } catch (_: Exception) { false }
    }
    fun stop(){running=false;try{server?.close()}catch(_:Exception){};server=null;workers.shutdownNow()}
    private fun loop(){while(running)try{val s=server?.accept();if(s!=null)try{workers.execute{ s.soTimeout=30_000; handle(s) }}catch(_:java.util.concurrent.RejectedExecutionException){try{s.close()}catch(_:Exception){}}}catch(_:Exception){if(running)try{Thread.sleep(250)}catch(_:InterruptedException){Thread.currentThread().interrupt();return}}}
    private fun handle(s:Socket){s.use{socket->val i=BufferedInputStream(socket.getInputStream());val o=BufferedOutputStream(socket.getOutputStream());try{
        val first=line(i)?:return;val p=first.split(' ');if(p.size!=3){reply(o,400,"bad request");return}
        val method=p[0].uppercase();val uri=URI("http://127.0.0.1"+p[1]);val h=mutableMapOf<String,String>()
        while(true){val x=line(i)?:break;if(x.isEmpty())break;val k=x.indexOf(':');if(k>0)h[x.substring(0,k).lowercase()]=x.substring(k+1).trim()}
        if(method=="OPTIONS"){reply(o,403,"cross-origin access disabled");return}
        if(uri.path=="/healthz"){replyJson(o,200,"{\"ok\":true,\"service\":\"local-media\"}");return}
        if(!uri.path.startsWith("/media/")){reply(o,404,"not found");return}
        val key=URLDecoder.decode(uri.path.removePrefix("/media/"),"UTF-8");if(!safe(key)){reply(o,400,"invalid media key");return}
        val authorization = h["authorization"] ?: ""
        val token = if (authorization.startsWith("Bearer ")) authorization.removePrefix("Bearer ").trim() else ""
        if(!valid(method,key,token)){reply(o,401,"invalid media token");return}
        if(method=="PUT"){upload(i,o,h,key);return}
        if(method=="GET"||method=="HEAD"){download(o,h,key,method=="HEAD");return}
        reply(o,405,"method not allowed")
    }catch(_:Exception){try{reply(o,500,"media server error")}catch(_:Exception){}}}}
    private fun upload(i:InputStream,o:OutputStream,h:Map<String,String>,key:String){
        val n=h["content-length"]?.toLongOrNull()?:run{reply(o,411,"content-length required");return}
        if(n<1||n>MAX_OBJECT_BYTES){reply(o,413,"payload too large");return}
        val contentType=(h["content-type"]?:"").substringBefore(';').trim().lowercase()
        if(!allowedMediaType(contentType)){reply(o,415,"unsupported media type");return}
        val expected=(h["x-content-sha256"]?:"").lowercase()
        if(!expected.matches(Regex("[0-9a-f]{64}"))){reply(o,400,"X-Content-SHA256 is required");return}
        val card=storage.selectedCardInfo()?:run{reply(o,503,"selected SD card is unavailable");return}
        if(n>card.availableBytes-SAFETY_RESERVE_BYTES){reply(o,507,"insufficient SD-card capacity");return}
        if(storage.findFile(key)!=null){reply(o,409,"media object already exists");return}
        val f=try{storage.createFile(key,contentType)}catch(_:Exception){reply(o,409,"cannot create media object");return}
        try{
            val digest=MessageDigest.getInstance("SHA-256")
            val dst=context.contentResolver.openOutputStream(f.uri,"w")?:error("not writable")
            dst.use{copy(i,it,n,digest)}
            val actual=digest.digest().joinToString(""){"%02x".format(it.toInt() and 0xff)}
            if(!MessageDigest.isEqual(actual.toByteArray(),expected.toByteArray())){
                f.delete();reply(o,422,"media checksum mismatch");return
            }
            replyJson(o,201,"{\"ok\":true,\"bytes\":$n,\"sha256\":\"$actual\"}")
        }catch(_:Exception){f.delete();reply(o,500,"media write failed")}
    }
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
            val headers="HTTP/1.1 $status\r\nContent-Type: "+mime(key)+"\r\nContent-Length: $len\r\nAccept-Ranges: bytes\r\n"+rangeHeader+"Connection: close\r\n\r\n"
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
    private fun valid(m:String,k:String,t:String):Boolean{
        val p=t.split('.',limit=2);if(p.size!=2)return false
        val expiry=p[1].toLongOrNull()?:return false
        val now=System.currentTimeMillis()/1000
        if(expiry<=now||expiry>now+300)return false
        if(!p[0].matches(Regex("[0-9a-f]{64}")))return false
        val secret=context.getSharedPreferences("node-config",0).getString("media_signing_secret","")?:""
        if(secret.length<32)return false
        val mac=Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(secret.toByteArray(StandardCharsets.UTF_8),"HmacSHA256"))
        val expected=mac.doFinal((m+"|"+k+"|"+expiry).toByteArray()).joinToString(""){"%02x".format(it)}
        return MessageDigest.isEqual(expected.toByteArray(),p[0].toByteArray())
    }
    private fun safe(k:String):Boolean {
        if(k.isBlank()||k.startsWith("/")||k.length>512||k.contains('\\'))return false
        val parts=k.split('/')
        return parts.size in 1..8&&parts.all{it.isNotBlank()&&it!="."&&it!=".."&&it.length<=128&&it.matches(Regex("[A-Za-z0-9._-]+"))}
    }
    private fun allowedMediaType(type:String)=type in setOf(
        "image/jpeg","image/png","image/webp","image/gif","image/avif",
        "video/mp4","video/webm","audio/mpeg","audio/mp4","audio/ogg","audio/webm"
    )
    private fun mime(k:String)=when(k.substringAfterLast('.',"").lowercase()){"jpg","jpeg"->"image/jpeg";"png"->"image/png";"webp"->"image/webp";"mp4"->"video/mp4";"m3u8"->"application/vnd.apple.mpegurl";"ts"->"video/mp2t";else->"application/octet-stream"}
    private fun copy(i:InputStream,o:OutputStream,n:Long,digest:MessageDigest){var l=n;val b=ByteArray(65536);while(l>0){val k=i.read(b,0,minOf(b.size.toLong(),l).toInt());if(k<0)error("short upload");digest.update(b,0,k);o.write(b,0,k);l-=k}}
    private fun line(i:InputStream):String?{val b=ByteArrayOutputStream();while(true){val x=i.read();if(x<0)return null;if(x==10)break;if(x!=13)b.write(x);if(b.size()>8192)error("header too large")};return b.toString("UTF-8")}
    private fun reply(o:OutputStream,s:Int,b:String){o.write(("HTTP/1.1 "+s+" Error\r\nContent-Type: text/plain\r\nContent-Length: "+b.length+"\r\nConnection: close\r\n\r\n"+b).toByteArray());o.flush()}
    private fun replyJson(o:OutputStream,s:Int,b:String){o.write(("HTTP/1.1 "+s+" OK\r\nContent-Type: application/json\r\nContent-Length: "+b.toByteArray(StandardCharsets.UTF_8).size+"\r\nConnection: close\r\n\r\n"+b).toByteArray(StandardCharsets.UTF_8));o.flush()}
    companion object {
        private const val MAX_OBJECT_BYTES = 500L * 1024L * 1024L
        private const val SAFETY_RESERVE_BYTES = 32L * 1024L * 1024L
    }
}