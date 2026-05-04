const jwt = require('jsonwebtoken');
const config = require('../../config/app');
const AppResponse = require('../response/AppResponse');

function authenticate(req, res, next) {
  const authHeader = req.headers['authorization'];
  const token = authHeader && authHeader.split(' ')[1];

  if (!token) {
    return AppResponse.unauthorized(res, 'Token tidak ditemukan');
  }

  try {
    const decoded = jwt.verify(token, config.jwt.secret);
    req.user = decoded;
    next();
  } catch (error) {
    if (error.name === 'TokenExpiredError') {
      return AppResponse.unauthorized(res, 'Sesi kadaluarsa');
    }
    return AppResponse.unauthorized(res, 'Token tidak valid');
  }
}

function requireRole(role) {
  return (req, res, next) => {
    if (req.user.role !== role) {
      return AppResponse.forbidden(res, `Akses hanya untuk ${role}`);
    }
    next();
  };
}

function requireSuperadmin(req, res, next) {
  if (req.user.role !== 'superadmin') {
    return AppResponse.forbidden(res, 'Akses hanya untuk Super Admin');
  }
  next();
}

module.exports = { authenticate, requireSuperadmin, requireRole };