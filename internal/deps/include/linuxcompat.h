
// NOTE: Never actually included, only used for code generation

#ifndef QUICLY_LINUXCOMPAT_H
#define QUICLY_LINUXCOMPAT_H

struct iovec
{
    char*    iov_base;  /* Base address of a memory region for input or output */
    size_t   iov_len;   /* The size of the memory pointed to by iov_base */
};

#endif // QUICLY_LINUXCOMPAT_H
