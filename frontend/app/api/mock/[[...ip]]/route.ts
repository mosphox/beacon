/**
 * Development-only mock of the Go backend's JSON response.
 *
 * `proxy.ts` rewrites data requests here so the dev server reproduces production's
 * content negotiation on a single port: a browser navigation renders the page, and that
 * page's own fetch lands on this handler.
 */

import { NextResponse } from 'next/server';

import { build } from '@/lib/mock';

let spin = 0;

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ ip?: string[] }> },
): Promise<NextResponse> {
  // This endpoint ships in the production bundle but must never answer there: the Go
  // backend forwards unmatched browser navigations to Next, so an unguarded route would
  // serve fabricated lookups from the real origin.
  if (process.env.NEXT_PUBLIC_USE_MOCK !== '1') {
    return NextResponse.json({ detail: 'Not found' }, { status: 404 });
  }

  const { ip } = await params;
  const requested = ip?.length ? decodeURIComponent(ip.join('/')) : null;

  if (requested !== null && !looksLikeAddress(requested)) {
    return NextResponse.json({ detail: 'Invalid IP address format' }, { status: 404 });
  }

  // The root cycles through fixtures so every state is reachable by reloading.
  const body = build(requested, requested === null ? spin++ : 0);

  return NextResponse.json(body, {
    headers: { 'Cache-Control': 'no-cache, must-revalidate' },
  });
}

function looksLikeAddress(s: string): boolean {
  return /^[0-9.]+$/.test(s) || /^[0-9a-fA-F:.]+$/.test(s);
}
