const jwt = require('jsonwebtoken');
const bcrypt = require('bcryptjs');
const config = require('../config/app');
const User = require('../model/User');
const Token = require('../model/Token');

class AuthService {
  static async loginAdmin(username, password) {
    const user = await User.findByUsername(username);

    if (!user) {
      throw new Error('Username tidak ditemukan');
    }

    if (user.role !== 'superadmin') {
      throw new Error('Akses ditolak');
    }

    const match = await bcrypt.compare(password, user.password);
    if (!match) {
      throw new Error('Password salah');
    }

    const token = jwt.sign(
      { role: 'superadmin', type: 'admin', userId: user.id },
      config.jwt.secret,
      { expiresIn: config.jwt.expiresIn }
    );

    return { token, role: 'superadmin' };
  }

  static async loginWithToken(accessCode) {
    const tokenData = await Token.findByCode(accessCode);

    if (!tokenData) {
      throw new Error('Kode Akses tidak valid');
    }

    const allowedApps = typeof tokenData.allowed_apps === 'string'
      ? JSON.parse(tokenData.allowed_apps)
      : tokenData.allowed_apps;

    const token = jwt.sign(
      {
        role: 'user',
        type: 'token',
        token_id: tokenData.id,
        allowedApps,
      },
      config.jwt.secret,
      { expiresIn: '12h' }
    );

    return {
      token,
      role: 'user',
      label: tokenData.label,
      allowedApps,
    };
  }

  static verifyToken(token) {
    try {
      return jwt.verify(token, config.jwt.secret);
    } catch (error) {
      if (error.name === 'TokenExpiredError') {
        throw new Error('Sesi kadaluarsa');
      }
      throw new Error('Token tidak valid');
    }
  }
}

module.exports = AuthService;
