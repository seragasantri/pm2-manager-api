const Token = require('../model/Token');

class TokenService {
  static async getAllTokens() {
    return Token.all();
  }

  static async createToken(data, createdBy) {
    const { label, allowedApps } = data;

    if (!label || !allowedApps || allowedApps.length === 0) {
      throw new Error('Label dan minimal 1 aplikasi harus dipilih');
    }

    return Token.create({ label, allowed_apps: allowedApps, created_by: createdBy });
  }

  static async updateToken(id, data) {
    const { label, allowedApps } = data;
    const existing = await Token.findById(id);

    if (!existing) {
      throw new Error('Token tidak ditemukan');
    }

    // Token code cannot be changed, only label and allowed apps
    await Token.update(id, { label, allowed_apps: allowedApps });
    return Token.findById(id);
  }

  static async deleteToken(id) {
    const existing = await Token.findById(id);
    if (!existing) {
      throw new Error('Token tidak ditemukan');
    }
    return Token.delete(id);
  }
}

module.exports = TokenService;