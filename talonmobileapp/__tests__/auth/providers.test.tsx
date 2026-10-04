import React from 'react'
import { render, screen } from '@testing-library/react-native'
import AuthScreen from '@/app/auth/index'
import { authApi } from '@/lib/api'

// FM-PROVa — mobile half: Google's offer retires on BOTH sign-in surfaces.
// The provider entry and its G-mark branch survive in source (a return is
// an uncomment), but the rendered list must be exactly what the product
// backs: GitHub.
//
// Harness: the OAuth browser, redirect builder, provider fetch and the
// token self-heal are all seams — this pins what the surface OFFERS.
//
// Red before L64 (observed): "Continue with Google" renders beside GitHub.

jest.mock('react-native-safe-area-context', () =>
  jest.requireActual('react-native-safe-area-context/jest/mock').default,
)
jest.mock('react-native-reanimated', () => require('@/test/native-seams').reanimated())
jest.mock('expo-router', () => ({ useRouter: () => ({ push: jest.fn(), replace: jest.fn() }) }))
jest.mock('expo-web-browser', () => ({
  warmUpAsync: jest.fn(async () => {}),
  coolDownAsync: jest.fn(async () => {}),
  openAuthSessionAsync: jest.fn(async () => ({ type: 'dismiss' })),
}))
jest.mock('expo-constants', () => ({ appOwnership: 'storeClient' }))
jest.mock('expo-auth-session', () => ({ makeRedirectUri: () => 'talon://auth/callback' }))
jest.mock('@/lib/api', () => ({
  authApi: { providers: jest.fn(), startURL: jest.fn(), exchange: jest.fn() },
  meApi: { get: jest.fn() },
}))
jest.mock('@/lib/auth', () => ({ getToken: jest.fn(async () => null) }))

beforeEach(() => {
  jest.clearAllMocks()
})

it('L64 — the auth screen offers GitHub only, subtitle included', async () => {
  ;(authApi.providers as jest.Mock).mockResolvedValue({
    providers: [{ id: 'github', name: 'GitHub' }],
  })

  await render(<AuthScreen />)

  await screen.findByText('Continue with GitHub')
  expect(screen.queryByText('Continue with Google')).toBeNull()
  expect(screen.queryByText(/Google/)).toBeNull()
})
