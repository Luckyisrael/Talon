import { View, Text, StyleSheet } from 'react-native'

export default function HomeScreen() {
  return (
    <View style={styles.container}>
      <Text style={styles.title}>SeekerStudio</Text>
      <Text style={styles.subtitle}>Your app will appear here</Text>
      <Text style={styles.hint}>Use the AI assistant to start building</Text>
    </View>
  )
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: '#0a0a0a',
    padding: 24,
  },
  title: {
    color: '#ededed',
    fontSize: 28,
    fontWeight: 'bold',
    marginBottom: 8,
  },
  subtitle: {
    color: '#888',
    fontSize: 16,
    marginBottom: 16,
  },
  hint: {
    color: '#555',
    fontSize: 13,
  },
})
