#!/bin/bash
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
export ANDROID_HOME=/root/android-sdk
export PATH=/usr/bin:/usr/local/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$PATH
sdkmanager platform-tools "platforms;android-34" "build-tools;34.0.0"
