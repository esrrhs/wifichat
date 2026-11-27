plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "com.wirelessdebug"
    compileSdk = 37
    compileSdkMinor = 2

    defaultConfig {
        minSdk = 26
        consumerProguardFiles("proguard-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation("org.conscrypt:conscrypt-android:2.9.0")
}
