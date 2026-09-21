import BeaconView from '../beacon-view';

export default async function Page({
  params,
}: {
  params: Promise<{ ip?: string }>;
}) {
  const { ip } = await params;
  // Keyed on the address so navigating between lookups remounts into the loading
  // state rather than briefly showing the previous address's data.
  return <BeaconView key={ip ?? 'self'} />;
}
