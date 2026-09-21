// eslint-config-next 16 ships flat configs directly; FlatCompat is not needed and
// mis-handles its circular plugin references.
import coreWebVitals from 'eslint-config-next/core-web-vitals';
import typescript from 'eslint-config-next/typescript';

const config = [
  ...(Array.isArray(coreWebVitals) ? coreWebVitals : [coreWebVitals]),
  ...(Array.isArray(typescript) ? typescript : [typescript]),
  { ignores: ['.next/**', 'node_modules/**', 'next-env.d.ts'] },
];

export default config;
