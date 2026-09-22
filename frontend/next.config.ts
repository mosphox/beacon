import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  output: 'standalone',
  // Nothing gains from telling the internet what this is built with.
  poweredByHeader: false,
};

export default nextConfig;
