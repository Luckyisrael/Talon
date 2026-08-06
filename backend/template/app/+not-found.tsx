import { Link, Stack } from 'expo-router'
import { View, Text, StyleSheet } from 'react-native'

export default function NotFoundScreen() {
  return (
    <>
      <Stack.Screen options={{ title: 'Oops!' }} />
      <View style={styles.container}>
        <Text style={styles.title}>Page not found</Text>
        <Link href="/" style={styles.link}>
          Go to home screen
        </Link>
      </View>
    </>
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
    fontSize: 20,
    fontWeight: 'bold',
    marginBottom: 16,
  },
  link: {
    color: '#9945FF',
    fontSize: 14,
  },
})
