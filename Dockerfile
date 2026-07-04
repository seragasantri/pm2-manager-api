# Menggunakan Node.js versi 24 LTS berbasis Alpine
FROM node:24-alpine

WORKDIR /app

# Menyalin package.json dan package-lock.json
COPY package*.json ./
# Menginstal dependensi produksi (agar ukuran container tetap kecil)
RUN npm install --production

# Menyalin seluruh kode sumber backend
COPY . .

# --- CATATAN PORT ---
# Ganti angka 3004 di bawah ini sesuai dengan PORT yang digunakan oleh Express.js Anda
# (Misalnya jika ExpressJS Anda berjalan di port 5000, 8000, atau 8080)
EXPOSE 3003

# Menjalankan aplikasi backend
CMD ["npm", "start"]