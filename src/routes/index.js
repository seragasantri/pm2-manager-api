const express = require('express');
const AuthMiddleware = require('../http/middleware/AuthMiddleware');
const AuthController = require('../controller/AuthController');
const AppController = require('../controller/AppController');
const TokenController = require('../controller/TokenController');
const FileController = require('../controller/FileController');

const router = express.Router();

// Public routes
router.post('/auth/login', AuthController.login);

// Protected routes
router.use(AuthMiddleware.authenticate);

router.get('/auth/me', AuthController.me);

// App management
router.get('/apps', AppController.index);
router.post('/apps/action', AppController.action);

// Token management (admin only)
router.get('/tokens', AuthMiddleware.requireRole('superadmin'), TokenController.index);
router.post('/tokens', AuthMiddleware.requireRole('superadmin'), TokenController.store);
router.put('/tokens/:id', AuthMiddleware.requireRole('superadmin'), TokenController.update);
router.delete('/tokens/:id', AuthMiddleware.requireRole('superadmin'), TokenController.destroy);

// File manager
router.get('/files', FileController.index);
router.post('/files/read', FileController.read);
router.post('/files/write', FileController.write);
router.post('/files/delete', FileController.delete);
router.post('/files/create-dir', FileController.createDir);

module.exports = router;
