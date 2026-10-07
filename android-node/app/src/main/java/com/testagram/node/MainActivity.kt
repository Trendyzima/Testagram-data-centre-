package com.testagram.node
import android.app.Activity
import android.os.Bundle
import android.widget.*
import java.io.File
class MainActivity:Activity(){
 override fun onCreate(b:Bundle?){super.onCreate(b);val root=File(filesDir,"node-data/volumes");root.mkdirs()
 val text=TextView(this).apply{setPadding(32,48,32,32);text=describe(root)}
 val refresh=Button(this).apply{text="Refresh local storage";setOnClickListener{textView->text.text=describe(root)}}
 setContentView(LinearLayout(this).apply{orientation=LinearLayout.VERTICAL;addView(text);addView(refresh)})}
 private fun describe(root:File):String{val s=root.statfs();return "Testagram Node\n\nAndroid app-private storage\n"+root.absolutePath+"\nTotal: "+(s.blockCountLong*s.blockSizeLong/(1024*1024))+" MB\nFree: "+(s.availableBlocksLong*s.blockSizeLong/(1024*1024))+" MB"}
}
