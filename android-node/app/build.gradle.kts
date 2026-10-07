plugins { id("com.android.application"); id("org.jetbrains.kotlin.android") }
android { namespace="com.testagram.node"; compileSdk=35
 defaultConfig { applicationId="com.testagram.node"; minSdk=26; targetSdk=35; versionCode=1; versionName="0.1.0" } }
dependencies { implementation("androidx.documentfile:documentfile:1.0.1") }
kotlin { jvmToolchain(17) }
