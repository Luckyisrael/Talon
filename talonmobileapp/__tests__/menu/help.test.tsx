import React from 'react'
import { render, screen } from '@testing-library/react-native'
import HelpScreen from '@/app/menu/help'

// FM-DEVf(iii) — the FAQ described only pairing a SECOND phone, so on one
// device the user had no answer for the path they are actually on: no
// scanning is possible when there is one screen. The help surface has to say
// how the project gets handed to the dev client without one.
//
// Harness: expo-router is mocked to plain router fns (MenuScreen presses
// Back), safe-area rides the shipped jest mock as in device.test.tsx.
//
// Red before L34 (observed): no entry answers "test on this phone".
jest.mock('react-native-safe-area-context', () =>
  jest.requireActual('react-native-safe-area-context/jest/mock').default,
)
jest.mock('expo-router', () => ({
  useRouter: () => ({ push: jest.fn(), back: jest.fn() }),
}))

it('answers how to test on this phone (FM-DEVf)', async () => {
  await render(<HelpScreen />)
  await screen.findByText('How do I test on this phone?')
  expect(screen.getByText(/Open on this phone/)).toBeTruthy()
})
