#!/bin/bash
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
export ANDROID_HOME=/root/android-sdk
export PATH=/usr/bin:/usr/local/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$JAVA_HOME/bin:$PATH

# Persist env vars
cat >> /root/.bashrc << 'ENVEOF'
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
export ANDROID_HOME=/root/android-sdk
export PATH=/usr/bin:/usr/local/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$JAVA_HOME/bin:$PATH
ENVEOF

echo "=== Java ==="
java -version 2>&1
echo "=== ADB ==="
$ANDROID_HOME/platform-tools/adb devices
echo "=== SDK ==="
ls $ANDROID_HOME/platforms/
ls $ANDROID_HOME/build-tools/
