const AuthService = require('../service/AuthService');
const AppResponse = require('../http/response/AppResponse');

class AuthController {
  static async login(req, res) {
    try {
      const { username, password, accessCode } = req.body;

      // Super Admin login
      if (username && password) {
        const result = await AuthService.loginAdmin(username, password);
        return AppResponse.success(res, result, 'Login berhasil');
      }

      // Token login
      if (accessCode) {
        const result = await AuthService.loginWithToken(accessCode);
        return AppResponse.success(res, result, 'Login berhasil');
      }

      return AppResponse.validationError(res, { general: 'username/password atau accessCode diperlukan' });
    } catch (error) {
      return AppResponse.unauthorized(res, error.message);
    }
  }

  static async me(req, res) {
    try {
      return AppResponse.success(res, {
        role: req.user.role,
        allowedApps: req.user.allowedApps || [],
      });
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }
}

module.exports = AuthController;