.PHONY: all build test clean run android android-apk android-aab

BINARY=bin/wifichat

all: build

build:
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/wifichat

test:
	go test -v -race ./...

clean:
	rm -rf bin/ downloads/ android/app/libs/*.aar android/app/libs/*.jar

run: build
	./$(BINARY)

# Cross-compile Go engine AAR (requires Android NDK + gomobile).
android:
	bash android/scripts/build_mobile.sh

# Build debug APK (runs gomobile bind first).
android-apk: android
	cd android && ./gradlew :app:assembleDebug

# Build signed release AAB for Google Play (runs gomobile bind first).
android-aab: android
	cd android && ./gradlew :app:bundleRelease
