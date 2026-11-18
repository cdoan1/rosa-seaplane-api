import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  server: {
    // The web app calls the custom-authorizer API Gateway directly (CORS
    // enabled there); no BFF proxy. Local development points VITE_* env
    // vars at the deployed gateway and Keycloak.
  },
});
