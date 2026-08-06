import { Tabs } from 'expo-router'
import { View, Text, StyleSheet } from 'react-native'

export default function TabLayout() {
  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarStyle: { display: 'none' },
      }}
    >
      <Tabs.Screen name="index" />
    </Tabs>
  )
}
