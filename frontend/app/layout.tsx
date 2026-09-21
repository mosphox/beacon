import type { Metadata, Viewport } from 'next';
import { JetBrains_Mono, Martian_Mono, Outfit } from 'next/font/google';

import './globals.css';

const outfit = Outfit({
  subsets: ['latin'],
  weight: ['300', '600'],
  variable: '--font-outfit',
  display: 'swap',
});

const jetbrainsMono = JetBrains_Mono({
  subsets: ['latin'],
  weight: ['400', '700'],
  variable: '--font-jetbrains',
  display: 'swap',
});

// Candidate display face for the address, used by the /lab variants.
const martianMono = Martian_Mono({
  subsets: ['latin'],
  weight: ['300', '700'],
  variable: '--font-martian',
  display: 'swap',
});

export const metadata: Metadata = {
  title: 'Beacon',
  description: 'Your IP address, and everything a server can tell about your connection.',
};

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  themeColor: '#0f0f23',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      className={`${outfit.variable} ${jetbrainsMono.variable} ${martianMono.variable}`}
      /* Dark-only: tells the UA to render scrollbars and form controls to match. */
      style={{ colorScheme: 'dark' }}
    >
      <head>
        <link rel="icon" href="/logo.png" />
      </head>
      <body>{children}</body>
    </html>
  );
}
