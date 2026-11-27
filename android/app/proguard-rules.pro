-keep class go.** { *; }
-keep class wifichat.** { *; }
-keep class com.esrrhs.wifichat.core.** { *; }
-dontwarn go.**
-keepclassmembers class * {
    native <methods>;
}
