/**
 * Development content negotiation, mirroring the Go backend.
 *
 * In production the backend serves the page and the data at the same URL and chooses by
 * Accept: a navigating browser gets HTML, that page's own fetch gets JSON. Without this,
 * local development would need a second origin and the fetch path would go untested.
 *
 * Inert in production — the container serves a build that the Go backend fronts, and this
 * only rewrites when the mock is explicitly enabled.
 */

import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';

export function proxy(request: NextRequest): NextResponse {
  if (process.env.NEXT_PUBLIC_USE_MOCK !== '1') {
    return NextResponse.next();
  }

  const wantsJson =
    request.headers.get('accept')?.includes('application/json') ||
    request.nextUrl.searchParams.get('format') === 'json';

  if (!wantsJson) {
    return NextResponse.next();
  }

  const path = request.nextUrl.pathname === '/' ? '' : request.nextUrl.pathname;
  return NextResponse.rewrite(new URL(`/api/mock${path}`, request.url));
}

export const config = {
  // Everything except Next's own assets and the mock endpoint itself.
  matcher: ['/((?!_next/|api/mock|favicon.ico|logo.png).*)'],
};
