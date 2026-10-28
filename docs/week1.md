Exercise 3:
    1. 资源单位是什么？thread-per-connection的资源单位是OS线程。goroutine的资源单位是用户态协程
    2. 阻塞是发生什么？ 不是不处理，而是占用了不返回。线程在Read()函数上挂起，不继续运动。goroutine在Read上阻塞（也可能在Write上阻塞）。
    3. 线程用一个链接一份栈换来编码简单。 event loop用编码复杂换来内存效率。（调用用户态协程在100ns级别，比陷入内核需要的10微秒更快。

