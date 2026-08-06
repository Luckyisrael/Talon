import React from "react";
import { View, Text, StyleSheet } from "react-native";

export default function App() {
  return (
    <View style={styles.container}>
      <Text style={styles.text}>SeekerStudio</Text>
      <Text style={styles.sub}>Your app will appear here</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    backgroundColor: "#0a0a0a",
  },
  text: {
    color: "#ededed",
    fontSize: 24,
    fontWeight: "bold",
  },
  sub: {
    color: "#888",
    fontSize: 14,
    marginTop: 8,
  },
});
